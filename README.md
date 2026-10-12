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
- **分享外链**：创建公开 / 加提取码 / 带有效期的分享链接，列出与撤销分享
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
      "driver": "139Yun",
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
> 只填 `Authorization` 就能完成全部功能（含空间查询）。配额接口
> `user-njs.yun.139.com/user/disk/quota/detail` 靠这个 cookie 鉴权，
> body 传空对象 `{}` 即可，不需要 `user_domain_id`。
>
> 如果同时提供 `Username`（手机号）和 `Password`，工具会自动完成密码登录流程，
> 获取完整 token 并设置 `UserDomainID`。

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
      "driver": "139Yun",
      "addition": {
        "Authorization": "...",
        "Username": "13800138000",
        "Password": "your-password",
        "RootFolderID": "",
        "Type": "personal_new"
      }
    }
  ],
  "default_mount": "/yun139"
}
```

> **`driver` 取值**：驱动实际注册的名字是 `139Yun`（见 `drivers/yun139/meta.go` 的
> `driver.Config.Name`）。`yun139` 作为别名同样可用，两种拼写都能启动。

> **字段名大小写不敏感**：`addition` 用 jsoniter 的
> `ConfigCompatibleWithStandardLibrary` 解析，`Authorization` / `authorization`
> 等价。但键名必须真的存在 —— 例如自定义根目录的键是 `RootFolderID`，写 `RootID`
> 会被静默忽略。

### Addition 字段说明

| 字段 | 必填 | 说明 |
|------|------|------|
| `Authorization` | ✅ | 浏览器 Cookie 中的 Authorization token |
| `Username` | 可选 | 手机号，与 Password 配合使用可自动登录 |
| `Password` | 可选 | 登录密码，配合 Username 使用 |
| `RootFolderID` | 可选 | 自定义根目录 ID（`root_folder_id`），默认使用网盘根目录 |
| `Type` | 可选 | 账号类型：`personal_new` / `personal` / `family` / `group` / `share`。省略时 SDK 自动填 `personal_new`（驱动结构体上的 `default:"personal_new"` 只是给 Web 表单看的提示，JSON 反序列化不会应用）；**写错拼写不会被纠正**，会报 `not implement` |
| `UserDomainID` | 可选 | 用户域 ID（`user_domain_id`，浏览器 Cookie 里的 `ud_id`）。配额接口不依赖它，**不填也能正常用 `space`**。若填写则必须真实有效 —— 传一个错误的值会收到 `01000001 参数错误`，比不填更糟 |
| `MailCookies` | 可选 | 邮箱 Cookie，部分旧 API 需要 |
| `LinkID` | 可选 | 分享链接 ID |
| `CloudID` | 可选 | 群组 CloudID |

### 多账号

```json
{
  "storages": [
    {
      "mount_path": "/personal",
      "driver": "139Yun",
      "addition": { "Authorization": "token-personal-..." }
    },
    {
      "mount_path": "/family",
      "driver": "139Yun",
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
| `cloudfs space [mount]` | 查看空间用量（139Yun 按云盘类型分项，见下文 [空间](#空间)） |
| `cloudfs whoami [mount]` | 查看账户信息 |
| `cloudfs share <path> [--password] [--days N] [--name N] [--to p1,p2]` | 创建分享链接（**会把文件公开出去**） |
| `cloudfs shares [--type sent\|received] [--json]` | 列出我发出的 / 收到的分享 |
| `cloudfs unshare <linkID\|URL> [-f]` | 撤销分享（默认二次确认；也可直接粘整个分享链接） |
| `cloudfs init [--driver X] [--write]` | 打印/写入示例配置 |

全局选项：`-c, --config <path>` 指定配置文件路径（默认 `~/.cloudfs.json`）。

### 分享

```bash
# 永久链接，无提取码（最宽松）
cloudfs share /yun139/相册/2026.jpg

# 加提取码，7 天有效；提取码由 139 生成，和链接一起打印
cloudfs share /yun139/相册 --password --days 7 --name 我的相册

# 好友专享（pubType 2），viewers 传手机号
cloudfs share /yun139/相册 --to 13800138000,13900139000

# 机器可读输出
cloudfs share /yun139/相册 --json
```

`--days` 只接受 `0`（永久）、`1`、`7` —— 这是 139 服务端给出的全部选项，传别的值
会在本地直接报错，不会打到服务端。

```bash
cloudfs shares                      # 我发出的分享（link 列打印完整 id，可直接复制）
cloudfs shares --type received      # 别人发给我的
cloudfs shares --json               # 完整 JSON，含每个分享的 url
cloudfs unshare A1b2C3d4E5f6G       # 撤销（要输 y）
cloudfs unshare A1b2C3d4E5f6G -f    # 跳过确认
# 直接粘整个链接也行，自动解析出 link id：
cloudfs unshare "https://yun.139.com/shareweb/#/w/i/A1b2C3d4E5f6G" -f
```

> **分享需要个人账号**。外链服务挂在 139 的 personalCloud 编排网关上，只有
> `personal_new` / `personal` 能用；家庭云、群组云会直接报
> `sharing is not supported for account type ...`。
> 注意这和 `Type: "share"` 是两回事 —— 后者是"消费"别人发来的分享（走 AES 分享网关），
> 前者是"自己创建"分享。

### 空间

`cloudfs space` 对 139Yun 打印的是 139 自己的配额报表（MB 原值 + GiB 折算列），
并**按云盘类型分项**：

```
mount:  /yun139

┌────────────────────────────┬──────────┬────────────┐
│                            │    MB    │     ≈     │
├────────────────────────────┼──────────┼────────────┤
│ 总配额 diskSize            │   20,480 │     20 GiB │
├────────────────────────────┼──────────┼────────────┤
│ 个人云（我的文件）usedSize │  196,894 │  192.3 GiB │
├────────────────────────────┼──────────┼────────────┤
│ 家庭云usedSize             │   26,079 │   25.5 GiB │
├────────────────────────────┼──────────┼────────────┤
│ 共享群usedSize             │        0 │      0 GiB │
├────────────────────────────┼──────────┼────────────┤
│ 圈子usedSize               │        0 │      0 GiB │
├────────────────────────────┼──────────┼────────────┤
│ 笔记usedSize               │        0 │      0 GiB │
├────────────────────────────┼──────────┼────────────┤
│ 群组usedSize               │        0 │      0 GiB │
├────────────────────────────┼──────────┼────────────┤
│ 合计已用                   │  222,973 │  217.7 GiB │
├────────────────────────────┼──────────┼────────────┤
│ freeDiskSize               │ −202,493 │ −197.7 GiB │
└────────────────────────────┴──────────┴────────────┘
单位 MB（1 GiB = 1024 MB）；合计已用 = 总配额 − freeDiskSize，
freeDiskSize 为负即已超额。139 没有按目录的配额。
```

几点值得注意：

- **没有"某个目录还剩多少容量"这个概念**。139 前端的状态里只有账号级配额和
  `quotaList`（按 driveType 分项），没有任何按路径聚合的字段，所以 `space`
  不接受路径参数。
- **分项之和可以超过总配额**。家庭云和个人云吃的是同一个池子，所以上面这个
  20 GiB 的账号实际用了 217.7 GiB，`freeDiskSize` 是负的。上传被拒报的
  `00010012 资源配额不足` 就是这种情况。
- **`quotaList` 不做零值过滤**，六类云盘全部打印，表形状不会随账号变化而变。

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
    // 上传；最后一个参数是进度回调，不需要就传 nil
    client.Upload(ctx, "/yun139/我的文件", "/tmp/photo.jpg", nil)

    // 查看空间（任意驱动的通用三数，字节）
    sp, _ := client.Space(ctx, "/yun139")
    fmt.Printf("Total: %d, Used: %d, Free: %d\n", sp.Total, sp.Used, sp.Free)

    // 139 按云盘类型分项的配额（MB）。只有 139Yun 支持，其它驱动返回 error。
    // 分项之和可能大于 DiskSizeMB：家庭云和个人云共用同一个池子。
    q, err := client.Quota(ctx, "/yun139")
    if err != nil {
        panic(err)
    }
    fmt.Printf("配额 %d MB，已用 %d MB，剩余 %d MB\n",
        q.DiskSizeMB, q.DiskSizeMB-q.FreeDiskSizeMB, q.FreeDiskSizeMB)
    for _, it := range q.Items {
        fmt.Printf("  %s: %d MB\n", it.Name, it.UsedMB)
    }

    // 账户信息
    info, _ := client.UserInfo(ctx, "/yun139")
    fmt.Printf("Account: %s, Nickname: %s\n", info.Account, info.Nickname)

    // 分享：这会生成一个任何人都能拿 URL 下载的链接，调用方必须自己决定要不要调
    res, err := client.Share(ctx, "/yun139/我的文件/photo.jpg", cloudfs.ShareOptions{
        Encrypted:   true, // 加提取码，码由 139 生成
        ExpiresDays: 7,    // 0=永久, 1, 7
        Name:        "相册",
    })
    if err != nil {
        panic(err)
    }
    fmt.Printf("link=%s code=%s\n", res.URL, res.Passwd)

    // 列出并撤销
    page, _ := client.ListShares(ctx, "/yun139", cloudfs.ShareKindSent, 1, 100)
    for _, it := range page.Items {
        fmt.Printf("%s  %s  %s\n", it.LinkID, it.Name, it.URL)
    }
    client.CancelShare(ctx, "/yun139", res.LinkID)
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

> **`Type: "share"` 不等于"能分享"**。`share` 类型是用来**读取别人发来的分享**
> （AES 加密分享网关）。**自己创建分享链接**走的是完全不同的 personalCloud
> 编排网关，需要 `personal_new` / `personal`；家庭云、群组云创建分享会被驱动
> 直接拒绝。见上文 [分享](#分享)。

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
