package config

import "time"

type Config struct {
    PublicPort       int
    AdminPort        int
    TestInterval     time.Duration
    TestTimeout      time.Duration
    TestConcurrency  int
    GitHubProxy      string // 代理地址，如 https://ghproxy.com/
}

func New() *Config {
    return &Config{
        PublicPort:      12345,
        AdminPort:       23456,
        TestInterval:    24 * time.Hour,
        TestTimeout:     5 * time.Second,
        TestConcurrency: 10,
        GitHubProxy:     "",
    }
}
