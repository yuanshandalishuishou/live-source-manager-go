module github.com/yuanshandalishuishou/live-source-manager-go

go 1.21

require (
    github.com/gin-gonic/gin v1.9.1
    github.com/go-ini/ini v1.67.0
    github.com/golang-jwt/jwt/v5 v5.0.0
    github.com/gorilla/websocket v1.5.0
    github.com/mattn/go-sqlite3 v1.14.17
    github.com/robfig/cron/v3 v3.0.1
    github.com/schollz/progressbar/v3 v3.13.1
    github.com/zu1k/nali v0.7.0
    golang.org/x/crypto v0.14.0
    golang.org/x/net v0.17.0
    gopkg.in/yaml.v3 v3.0.1
)
# 获取最新版本的 nali 库
go get github.com/zu1k/nali@latest

# 清理未使用的依赖并更新 go.sum
go mod tidy

# 格式化代码
go fmt ./...
