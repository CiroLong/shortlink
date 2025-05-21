# 📎 ShortLink
一个基于 Go 的高性能短链接服务，支持短链接生成、跳转、访问统计等功能，适用于高并发场景。

## 🚀 功能特性
### 🔗 短链接生成：将长链接压缩为唯一短码

### 📈 访问统计：记录每个短链接的访问次数

### ⚡ 高并发优化：使用 Redis 缓存计数，异步批量写入数据库

### 🧹 定时清理：自动清理过期链接和缓存数据

### 🐳 容器化部署：提供 Docker 支持，快速部署

## 🛠 技术栈
**语言**：Go 1.20+

**Web 框架**：Gin

**数据库**：MySQL

**缓存**：Redis

**ORM**：GORM

**容器化**：Docker & Docker Compose

## 📁 项目结构
```bash
shortlink/
├── config/             # 配置文件和初始化
├── src/
│   ├── handler/        # 路由处理器
│   ├── model/          # 数据模型
│   ├── service/        # 业务逻辑
│   └── utils/          # 工具函数
├── main.go             # 应用入口
├── Dockerfile          # Docker 构建文件
├── docker_compose_config.yml # Docker Compose 配置
├── go.mod              # Go 模块文件
└── README.md           # 项目说明