# cloudfs-139

[![Go](https://img.shields.io/badge/go-1.25%2B-blue.svg)](https://go.dev/)
[![License](https://img.shields.io/badge/license-MIT-lightgrey.svg)](LICENSE)

中国移动 139 网盘（yun.139.com）的独立 Go SDK 与命令行工具。

基于 [OpenList](https://github.com/OpenListTeam/OpenList) 驱动框架，支持 personal_new / personal / family / group / share 五种账号类型。

---

## 功能

- **文件操作**：列表、下载、上传、创建目录、删除、移动、复制、重命名
- **空间查询**：查看已用 / 剩余 / 总容量
- **账户信息**：获取当前登录账号（手机号）、昵称、VIP 状态
- **多账号支持**：一个配置文件中管理多个 139 网盘账号
- **离线 Token 刷新**：默认离线刷新，更安全
- **Go SDK**：可直接在 Go 项目中 import 使用

---

## 安装

### 方式一：从源码构建

```bash
go install github.com/milin2436/cloudfs-139/cmd/cloudfs@latest
```

或：

```bash
git clone https://github.com/milin2436/cloudfs-139.git
cd cloudfs-139
go build -o cloudfs ./cmd/cloudfs/
```

### 方式二：作为 Go 库

```bash
go get github.com/milin2436/cloudfs-139/sdk/cloudfs
```

---

## 快速开始

### 1. 生成配置文件

```bash
cloudfs init --driver yun139 --write
# 写入 ~/.cloudfs.json
```

### 2. 编辑配置

编辑 `~/.cloudfs.json`，填入你的 139 网盘 `Authorization` token：

```json
{
  "storages": [
    {
      "mount_path": "/yun139",
      "driver": "yun139",
      "addition": {
        "Authorization": "your-auth-token-here"
      }
    }
  ],
  "default_mount": "/yun139"
}
```

> **如何获取 Authorization token**：在浏览器中登录 [yun.139.com](https://yun.139.com)，
> 从 DevTools / Network 面板的 Cookie 中复制 `Authorization` 字段。
>
> 如果同时提供 `Username`（手机号）和 `Password`，工具会自动完成密码登录流程，
> 获取完整 token 并设置 `UserDomainID`，从而解锁空间查询等高级功能。

### 3. 使用 CLI

```bash
# 查看账户信息
cloudfs whoami

# 列出根目录
cloudfs ls /

# 下载文件
cloudfs get /我的文件/photo.jpg -o ./photo.jpg

# 上传文件
cloudfs put /我的文件 ./photo.jpg

# 创建目录
cloudfs mkdir /我的文件/2026

# 查看空间
cloudfs space

# 使用指定配置文件
cloudfs --config /path/to/config.json ls /
```

---

## 配置文件格式

```json
{
  "storages": [
    {
      "mount_path": "/yun139",
      "driver": "yun139",
      "addition": {
        "Authorization": "...",
        "Username": "13800138000",
        "Password": "your-password",
        "RootID": "",
        "Type": "personal_new"
      }
    }
  ],
  "default_mount": "/yun139"
}
```

### Addition 字段说明

| 字段 | 必填 | 说明 |
|------|------|------|
| `Authorization` | ✅ | 浏览器 Cookie 中的 Authorization token |
| `Username` | 可选 | 手机号，与 Password 配合使用可自动登录 |
| `Password` | 可选 | 登录密码，配合 Username 使用 |
| `RootID` | 可选 | 自定义根目录 ID，默认使用网盘根目录 |
| `Type` | 可选 | 账号类型：`personal_new`（默认）/ `personal` / `family` / `group` / `share` |
| `UserDomainID` | 可选 | 用户域 ID，自动登录后自动填充；提供后可查询空间 |
| `MailCookies` | 可选 | 邮箱 Cookie，部分旧 API 需要 |
| `LinkID` | 可选 | 分享链接 ID |
| `CloudID` | 可选 | 群组 CloudID |

### 多账号

```json
{
  "storages": [
    {
      "mount_path": "/personal",
      "driver": "yun139",
      "addition": { "Authorization": "token-personal-..." }
    },
    {
      "mount_path": "/family",
      "driver": "yun139",
      "addition": { "Authorization": "token-family-...", "Type": "family" }
    }
  ],
  "default_mount": "/personal"
}
```

使用 `mount_path` 前缀区分不同账号：`cloudfs ls /family`。

---

## CLI 命令

| 命令 | 说明 |
|------|------|
| `cloudfs ls <path>` | 列出目录内容 |
| `cloudfs get <path> [-o dest]` | 下载文件（默认输出到 stdout） |
| `cloudfs put <remote-dir> <local-file>` | 上传文件 |
| `cloudfs mkdir <path>` | 创建目录 |
| `cloudfs rm <path> [-f]` | 删除文件/目录（`-f` 跳过确认） |
| `cloudfs mv <src> <dst-dir>` | 移动 |
| `cloudfs cp <src> <dst-dir>` | 复制 |
| `cloudfs rename <path> <new-name>` | 重命名 |
| `cloudfs space [mount]` | 查看空间用量 |
| `cloudfs whoami [mount]` | 查看账户信息 |
| `cloudfs init [--driver X] [--write]` | 打印/写入示例配置 |

全局选项：`-c, --config <path>` 指定配置文件路径（默认 `~/.cloudfs.json`）。

---

## Go SDK 用法

```go
package main

import (
    "context"
    "fmt"
    "os"

    "github.com/milin2436/cloudfs-139/sdk/cloudfs"
)

func main() {
    ctx := context.Background()

    // 从配置文件加载
    client, err := cloudfs.NewClientFromFile(ctx, "")
    if err != nil {
        panic(err)
    }

    // 列出文件
    files, err := client.List(ctx, "/yun139/我的文件")
    if err != nil {
        panic(err)
    }
    for _, f := range files {
        fmt.Printf("%s\t%d\t%s\n", f.Name, f.Size, f.Type)
    }

    // 下载
    client.Download(ctx, "/yun139/我的文件/photo.jpg", os.Stdout)

    // 上传
    client.Upload(ctx, "/yun139/我的文件", "/tmp/photo.jpg")

    // 查看空间
    sp, _ := client.Space(ctx, "/yun139")
    fmt.Printf("Total: %d, Used: %d, Free: %d\n", sp.Total, sp.Used, sp.Free)

    // 账户信息
    info, _ := client.UserInfo(ctx, "/yun139")
    fmt.Printf("Account: %s, Nickname: %s\n", info.Account, info.Nickname)
}
```

---

## 支持的账号类型

| 类型 | 说明 | API 风格 |
|------|------|---------|
| `personal_new` | 新版个人网盘 | 新 API（X-Yun 头） |
| `personal` | 旧版个人网盘 | 旧 API（mcloud-sign 签名） |
| `family` | 家庭云 | 旧 API + 相册 AES 加密 |
| `group` | 群组云 | 旧 API + 相册 AES 加密 |
| `share` | 分享链接 | AES 加密分享 API |

---

## 架构

```
┌──────────────────────────────────────────────────────┐
│                    使用层                              │
│  ┌─────────────┐        ┌────────────────────────┐  │
│  │ cmd/cloudfs │        │   sdk/cloudfs (SDK)    │  │
│  │   (CLI)     │        │  Client.List/Get/Put   │  │
│  └──────┬──────┘        └──────────┬─────────────┘  │
│         │                          │                  │
│         └────────────┬─────────────┘                  │
│                      ▼                                │
│  ┌────────────────────────────────────────────────┐  │
│  │              cloudfso/op/                       │  │
│  │   操作层：List/Get/Link/Put/Mkdir/Remove/...     │  │
│  └────────────────────┬───────────────────────────┘  │
│                       ▼                               │
│  ┌────────────────────────────────────────────────┐  │
│  │           drivers/yun139/                       │  │
│  │   139Yun 驱动：HTTP 请求、登录、加密、Token 刷新    │  │
│  └────────────────────┬───────────────────────────┘  │
│                       ▼                               │
│  ┌────────────────────────────────────────────────┐  │
│  │              cloudfso/                          │  │
│  │   base/ HTTP 客户端   model/ 领域模型             │  │
│  │   db/ SQLite         utils/ 通用工具              │  │
│  │   driver/ 接口定义   conf/ 全局配置               │  │
│  │   stream/ 流处理     cache/ 缓存                 │  │
│  │   ... 共 25 个子包                              │  │
│  └────────────────────────────────────────────────┘  │
│                       ▼                               │
│         中国移动 139 网盘 API (yun.139.com)             │
└──────────────────────────────────────────────────────┘
```

---

## 开发

```bash
# 整理依赖
go mod tidy

# 构建 CLI
go build -o cloudfs ./cmd/cloudfs/

# 运行 CLI
./cloudfs --help
```

详细的开发文档见 [DEV.md](DEV.md)。

---

## License

MIT
