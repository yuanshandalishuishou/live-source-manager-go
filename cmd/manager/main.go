// Package main 是直播源管理工具的主入口，负责初始化所有模块、协调工作流程、
// 启动 Web 服务、设置定时任务，并处理操作系统信号以实现优雅关闭。
package main

import (
	"context"
	"flag"
	"os"
	"os/signal"
	"syscall"
	"time"

	"live-source-manager-go/internal/config"
	"live-source-manager-go/internal/db"
	"live-source-manager-go/internal/epg"
	"live-source-manager-go/internal/filter"
	"live-source-manager-go/internal/generator"
	"live-source-manager-go/internal/geo"
	"live-source-manager-go/internal/rules"
	"live-source-manager-go/internal/rtmp"
	"live-source-manager-go/internal/source"
	"live-source-manager-go/internal/tester"
	"live-source-manager-go/internal/web"
	"live-source-manager-go/pkg/logger"

	"github.com/robfig/cron/v3"
)

var (
	configPath = flag.String("config", "/config/config.ini", "配置文件路径")
	runOnce    = flag.Bool("once", false, "运行一次后退出")
	version    = flag.Bool("version", false, "显示版本信息")
)

const (
	appName    = "live-source-manager"
	appVersion = "2.0.0"
)

func main() {
	flag.Parse()

	if *version {
		printVersion()
		return
	}

	// 1. 加载配置文件
	cfg, err := config.Load(*configPath)
	if err != nil {
		panic("加载配置文件失败: " + err.Error())
	}

	// 2. 初始化日志
	logLevel := logger.Level(cfg.Logging.GetLogLevel())
	log, err := logger.New(cfg.Logging.File, logLevel)
	if err != nil {
		panic("初始化日志失败: " + err.Error())
	}
	log.Info("=== %s v%s 启动 ===", appName, appVersion)

	// 3. 初始化数据库
	dbPath := "./db/live-source.db"
	database, err := db.InitDB(dbPath)
	if err != nil {
		log.Fatal("初始化数据库失败: %v", err)
	}
	defer database.Close()
	log.Info("数据库初始化成功: %s", dbPath)

	// 4. 初始化归属地解析器
	geoResolver, err := geo.NewResolver("./data", log)
	if err != nil {
		log.Warn("初始化归属地解析器失败（将跳过归属地识别）: %v", err)
		geoResolver = nil
	}

	// 5. 初始化规则引擎
	rulesMgr, err := rules.NewManager(database, "/config/channel_rules.yml", log)
	if err != nil {
		log.Fatal("初始化规则引擎失败: %v", err)
	}

	// 6. 初始化源管理器
	sourceMgr := source.NewManager(cfg, log, database, rulesMgr)

	// 7. 初始化测试器
	testerInst := tester.NewTester(cfg, log, database, geoResolver, rulesMgr)

	// 8. 初始化筛选器
	filterInst := filter.NewFilter(cfg, log, database)

	// 9. 初始化生成器
	generatorInst := generator.NewGenerator(cfg, log, database)

	// 10. 初始化 RTMP 管理器（如果启用）
	var rtmpMgr *rtmp.Manager
	if cfg.RTMP.OpenRTMP {
		rtmpMgr = rtmp.NewManager(cfg, log, database)
		if err := rtmpMgr.Start(); err != nil {
			log.Error("启动 RTMP 管理器失败: %v", err)
		}
	}

	// 11. 初始化 EPG 管理器
	epgMgr := epg.NewManager(cfg, log, database)
	epgMgr.Start()

	// 12. 定义核心工作流程函数
	workflow := func() {
		log.Info("开始执行直播源更新工作流程...")
		start := time.Now()

		// 12.1 下载在线源
		sourceMgr.DownloadAll()

		// 12.2 解析所有源并入库
		if _, err := sourceMgr.ParseAll(); err != nil {
			log.Error("解析源失败: %v", err)
		}

		// 12.3 测试待测源（如果未在运行）
		if !testerInst.IsRunning() {
			taskID, err := testerInst.Start()
			if err != nil {
				log.Error("启动测试任务失败: %v", err)
			} else {
				log.Info("测试任务已启动，任务ID: %s", taskID)
				// 等待测试完成（如果是 runOnce 模式）
				if *runOnce {
					for testerInst.IsRunning() {
						time.Sleep(time.Second)
					}
				}
			}
		} else {
			log.Info("测试任务已在运行中，跳过本次测试")
		}

		// 12.4 获取活跃源并应用筛选
		activeSources, err := fetchActiveSources(database)
		if err != nil {
			log.Error("获取活跃源失败: %v", err)
			return
		}
		filteredSources := filterInst.HierarchicalFilter(activeSources)

		// 12.5 生成播放列表
		if err := generatorInst.Generate(); err != nil {
			log.Error("生成播放列表失败: %v", err)
		} else {
			generatorInst.UpdateTimestampFile()
		}

		// 12.6 可选：触发 RTMP 推流调度
		if rtmpMgr != nil {
			// 调度由 rtmpMgr 内部定时器处理，此处可主动触发一次
		}

		log.Info("工作流程执行完毕，耗时 %v", time.Since(start))
	}

	// 13. 立即执行一次工作流程
	workflow()

	// 如果是一次性运行模式，则退出
	if *runOnce {
		log.Info("一次性运行模式，程序退出")
		return
	}

	// 14. 启动 Web 管理界面
	webServer := web.NewServer(cfg, log, database, rulesMgr, sourceMgr, testerInst, filterInst, generatorInst, geoResolver)
	go func() {
		if err := webServer.Run(); err != nil {
			log.Fatal("Web 服务启动失败: %v", err)
		}
	}()

	// 15. 设置定时任务
	c := cron.New()
	cronExpr := cfg.System.CronExpression
	if cronExpr == "" {
		cronExpr = "0 2 * * *" // 默认每天凌晨 2 点
	}
	_, err = c.AddFunc(cronExpr, workflow)
	if err != nil {
		log.Error("添加定时任务失败: %v", err)
	} else {
		c.Start()
		log.Info("定时任务已设置: %s", cronExpr)
	}

	// 16. 等待退出信号
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	log.Info("收到退出信号，正在优雅关闭...")

	// 17. 清理资源
	c.Stop()
	if rtmpMgr != nil {
		rtmpMgr.Stop()
	}
	epgMgr.Stop()
	log.Info("程序已退出")
}

// fetchActiveSources 从数据库获取所有状态为 active 的源
func fetchActiveSources(db *sql.DB) ([]models.URLSourcePassed, error) {
	rows, err := db.Query(`SELECT id, url, name, tvg_id, tvg_logo, group_title, catchup, catchup_days, user_agent, source_type, raw_attributes, live_source_id, epg_id, epg_name, epg_logo, status, response_time_ms, resolution, bitrate, video_codec, audio_codec, frame_rate, download_speed, last_checked, fail_count, test_status, error_message, location, isp, extra_attrs, created_at, updated_at FROM url_sources_passed WHERE status = 'active'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sources []models.URLSourcePassed
	for rows.Next() {
		var src models.URLSourcePassed
		err := rows.Scan(
			&src.ID, &src.URL, &src.Name, &src.TvgID, &src.TvgLogo, &src.GroupTitle,
			&src.Catchup, &src.CatchupDays, &src.UserAgent, &src.SourceType,
			&src.RawAttributes, &src.LiveSourceID, &src.EPGID, &src.EPGName, &src.EPGLogo,
			&src.Status, &src.ResponseTimeMs, &src.Resolution, &src.Bitrate,
			&src.VideoCodec, &src.AudioCodec, &src.FrameRate, &src.DownloadSpeed,
			&src.LastChecked, &src.FailCount, &src.TestStatus, &src.ErrorMessage,
			&src.Location, &src.ISP, &src.ExtraAttrs, &src.CreatedAt, &src.UpdatedAt,
		)
		if err != nil {
			continue
		}
		sources = append(sources, src)
	}
	return sources, nil
}

func printVersion() {
	fmt.Printf("%s version %s\n", appName, appVersion)
}
