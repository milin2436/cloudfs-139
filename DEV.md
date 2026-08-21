# DEV.md — cloudfs-139 开发者文档

> 这份文档面向**修改者**：说明项目结构、关键实现路径、踩过的坑、构建与测试方式。
> README.md 面向使用者，本文面向维护者。

---

## 1. 项目概要

- **模块名**：`github.com/milin2436/cloudfs-139`
- **Go 版本**：1.25.0（开发机 Go 1.27.0，位于 `/home/super/bin/go/bin/go`）
- **来源**：从 [OpenList 4.2.5](https://github.com/OpenListTeam/OpenList) 移植
  - OpenList 源码：`/home/super/Downloads/code/OpenList-4.2.5`
  - 移植方式：将 OpenList 的 `internal/` 和 `drivers/base/` 复制到 `cloudfso/`，
    将所有 import 从 `github.com/OpenListTeam/OpenList/v4/...` 改为
    `github.com/milin2436/cloudfs-139/...`
- **用途**：独立的中国移动 139 网盘 SDK + CLI，不依赖 OpenList 主项目

### 为什么用 `cloudfso/` 而不是 `internal/`？

Go 的 `internal` 包只能被同一 module root 下的包导入。SDK 如果放在子 module 里会被拒绝。
因此我们把 OpenList 的 `internal/` **重新打包为普通包 `cloudfso/`**，这样 SDK 和 CLI
都可以自由 import。

---

## 2. 目录结构

```
cloudfs-139/
├── go.mod                  # 模块声明，28 个直接依赖
├── go.sum
├── DEV.md                  # ← 本文
├── README.md               # 面向用户
├── .gitignore
│
├── cloudfso/               # OpenList internal 的镜像（25 个子包）
│   ├── archive/tool/       # 压缩格式工具（zip/rar/7z 解压辅助）
│   ├── base/               # HTTP 客户端（resty 封装）、TLS、JSON 别名、上传限速、HTTP 抓包
│   ├── buffer/             # I/O 缓冲、pipe 包装
│   ├── cache/              # LRU / 类型缓存
│   ├── conf/               # 全局 conf.Conf（单例），Database 配置
│   ├── cron/               # 简单 cron（yun139 用，12h 刷新 token）
│   ├── db/                 # GORM + SQLite（纯 Go），存储记录/用户/设置/SSH 密钥
│   ├── driver/             # Driver 接口定义、driver.Config、进度追踪
│   ├── errs/               # 类型化错误（NotImplement/NotSupport/ObjectNotFound 等）
│   ├── generic_sync/       # sync.Map 包装
│   ├── http_range/         # HTTP Range 请求/读取支持
│   ├── hybrid_cache/       # 双层缓存（内存 + 文件）
│   ├── mem/                # 系统内存信息（Unix/Windows）
│   ├── model/              # 领域模型：Object/Storage/Link/ListArgs/FileStreamer 等
│   ├── net/                # HTTP 服务器辅助、OSS 客户端
│   ├── op/                 # ★ 操作层，SDK 调驱动的桥梁（List/Get/Lisk/Put/Mkdir/Remove/Move/Rename/Copy）
│   ├── singleflight/       # 请求去重
│   ├── stream/             # FileStream、RangeReadReadAtSeeker、流式上传
│   └── utils/              # 通用工具（JSON/Hash/Path/HTTP/String/OAuth2 等）+ random/
│
├── drivers/
│   └── yun139/             # ★ 139Yun 驱动实现
│       ├── meta.go         # Addition 结构体 + driver.Config + init() 注册
│       ├── types.go        # 所有 API 请求/响应结构体（455 行）
│       ├── driver.go       # Driver 接口实现（1167 行，5 种账号类型）
│       └── util.go         # HTTP 请求 + 登录/加密/Token 刷新（1766 行）
│
├── sdk/
│   └── cloudfs/            # ★ SDK 层
│       ├── doc.go          # 包文档 + 配置示例
│       ├── json.go         # JSON marshal/unmarshal 工具（防无限递归）
│       ├── config.go       # Config/StorageCfg/JSON 类型、Load/Marshal、ResolveConfigPath
│       └── client.go       # ★ SDK 核心（587 行）
│
└── cmd/
    └── cloudfs/            # ★ CLI
        ├── main.go         # Cobra 入口，注册 11 个子命令
        └── cmds.go         # 子命令实现
```

---

## 3. 关键实现路径

### 3.1 Client 初始化流程

入口：`sdk/cloudfs/client.go` → `NewClientFromFile(ctx, path)`

```
NewClientFromFile
  └─ LoadConfigFromFile        # 解析 JSON 配置
  └─ NewClient
      └─ onceInit()
          ├─ conf.Conf = &conf.Config{Database:{Type:"sqlite3"}}   # ①
          ├─ gorm.Open(sqlite.Open("file::memory:..."))            # ② 内存 SQLite
          ├─ db.Init(d)                                            # ③ AutoMigrate
          ├─ base.InitClient()                                     # ④ 初始化 resty 客户端
          └─ 遍历 storages
              └─ op.RegisterDriver(cfg.Driver, &driver{})          # ⑤ 注册驱动
              └─ op.MustSaveDriverStorage(storage)                 # ⑥ 写入 SQLite
              └─ driver.Init(ctx)                                  # ⑦ 驱动自己的初始化
```

**⑦ driver.Init 做了什么**（`drivers/yun139/driver.go`）：
1. 解析 `Authorization` token → 解码账户手机号
2. 如果有 `Username/Password` → 调用 `loginWithPassword` 获取完整 token
3. 查询路由策略（`/user/route/qryRoutePolicy`）→ 发现 API 主机地址
4. 根据账号类型（personal_new / personal / family / group / share）选择请求方式
5. 启动 12h cron 定时刷新 token

### 3.2 路径解析

`cloudfs Client` 接收的路径如 `/yun139/photos/a.jpg`，
由 `resolvePath()` 拆分为 `(mountPath="/yun139", relativePath="photos/a.jpg")`，
再通过 `driverForMount()` 找到对应的 Storage，交给 `op.*` 函数。

### 3.3 UserInfo 获取链路

`Client.UserInfo(ctx, mountPath)`:
1. `driverForMount` → 找到 yun139 驱动的 `*Yun139` 实例
2. 接口断言 `GetUserInfo(ctx)` → 调用 `drivers/yun139/driver.go`
3. `GetUserInfo` 内部：
   - 始终填充 `Account`（从 Authorization token 解码）
   - 始终填充 `Username`（来自 Addition）
   - 可选：尝试 `/hcy/user/getUserInfo` 端点获取 nickname / isVip
   - 可选：如果有 `UserDomainID`，调用 `getDiskQuotaDetail` 获取配额

**降级策略**：profile 端点 404 或 quota 失败都不会报错，只记录 debug 日志，
返回已有的 account/username 字段。

### 3.4 5 种账号类型

`drivers/yun139/util.go` 定义了不同的 HTTP 请求方式：

| 方法 | 账号类型 | 特点 |
|------|---------|------|
| `request()` | personal / family / group | 旧 API，`mcloud-sign` 签名头 |
| `personalPost()` / `newRequest()` | personal_new | 新 API，`X-Yun-*` 头 |
| `sharePost()` | share | AES 加密分享 API |
| `andAlbumRequest()` | family / group（相册） | AES-ECB 加密 |

`driver.go` 中 List/Get/Link/Put/MakeDir/Move/Rename/Copy/Remove 都有
`if d.isPersonalNew() { ... } else if d.isFamily() { ... }` 等多分支。

---

## 4. 踩过的坑（修改时必读）

### 4.1 `JSON.MarshalJSON` 无限递归

`type JSON map[string]any` 实现了 `MarshalJSON()`，如果内部直接 `json.Marshal(j)` 会无限递归。

**正确写法**（`sdk/cloudfs/config.go`）：

```go
func (j JSON) MarshalJSON() ([]byte, error) {
    return marshalValue(map[string]any(j))  // 显式转型到 map，断开递归
}
```

### 4.2 `cobra.MaximumArgs` 不存在

cobra v1.10.2 只有 `NoArgs` / `ExactArgs` / `ArbitraryArgs` / `OnlyValidArgs`，没有 `MaximumArgs`。

**正确写法**（`cmd/cloudfs/cmds.go`）：

```go
Args: func(cmd *cobra.Command, args []string) error {
    if len(args) > 1 {
        return fmt.Errorf("accepts at most 1 arg, received %d", len(args))
    }
    return nil
},
```

### 4.3 `http_range.Range` 是结构体

`rr.RangeRead(ctx, nil)` 编译不过，因为 `Range` 是 struct 不是 interface。

**正确写法**：`http_range.Range{}` 或 `http_range.Range{Start: 0, Length: -1}`

### 4.4 `go.mod` 版本修复历史

移植时 go.mod 的两个依赖版本有 404 问题：

| 原始版本 | 问题 | 修复后 |
|---------|------|--------|
| `bluemonday v0.0.0-20230110192803-c094f20a7ab0` | 伪版本不存在 | `v1.0.27` |
| `go4.org/readerutil v0.0.0-20170707121722-01e559f785a4` | 独立模块不存在 | `go4.org v0.0.0-20260112195520-a5071408f32f` |

来源：`/home/super/Downloads/code/OpenList-4.2.5/go.mod`

### 4.5 不能从外部 module import `internal`

如果将来想把某个包拆成独立 module，注意 `cloudfso/` 已经是普通包（非 internal），
可以安全地从任何 module import。

### 4.6 SQLite 驱动必须用 glebarez

- `gorm.io/driver/sqlite`（mattn）需要 CGO
- `github.com/glebarez/sqlite` 纯 Go

无 GCC 环境必须用 glebarez 版本。

---

## 5. 构建与测试

### 5.1 构建

```bash
cd /home/super/work/cloudfs-139

# 整理依赖
GOROOT=/home/super/bin/go go mod tidy

# 编译 CLI
GOROOT=/home/super/bin/go go build -o /tmp/cloudfs ./cmd/cloudfs/

# 编译 SDK 包
GOROOT=/home/super/bin/go go build ./sdk/cloudfs/
```

### 5.2 端到端测试

已有测试配置 `/tmp/139-personal-test.json`，包含一个 personal_new 账号的 Authorization：

```bash
/tmp/cloudfs --config /tmp/139-personal-test.json whoami
# → {"account":"...","username":"...","total_space":0,...}

/tmp/cloudfs --config /tmp/139-personal-test.json ls /
/tmp/cloudfs --config /tmp/139-personal-test.json space
```

### 5.3 调试模式

`ls --output raw` 会捕获 HTTP 请求/响应（通过 `base.EnableHTTPCapture()`），
输出原始 HTTP 交换内容，方便排查 API 问题。

---

## 6. 环境变量与开发机

| 变量 | 值 |
|------|----|
| GOROOT | `/home/super/bin/go` |
| Go 版本 | 1.27.0 |
| GOPROXY | 默认（goproxy.cn / proxy.golang.org） |
| OS | Linux amd64 |
| 原始 OpenList | `/home/super/Downloads/code/OpenList-4.2.5` |
| 测试配置 | `/tmp/139-personal-test.json` |

---

## 7. 与 OpenList 对照

| OpenList 路径 | cloudfs-139 路径 |
|---------------|------------------|
| `internal/op` | `cloudfso/op` |
| `internal/driver` | `cloudfso/driver` |
| `internal/model` | `cloudfso/model` |
| `internal/base` | `cloudfso/base` |
| `internal/conf` | `cloudfso/conf` |
| `internal/db` | `cloudfso/db` |
| `internal/utils` | `cloudfso/utils` |
| `internal/stream` | `cloudfso/stream` |
| `internal/http_range` | `cloudfso/http_range` |
| `internal/hybrid_cache` | `cloudfso/hybrid_cache` |
| `internal/cron` | `cloudfso/cron` |
| `internal/cache` | `cloudfso/cache` |
| `internal/buffer` | `cloudfso/buffer` |
| `internal/singleflight` | `cloudfso/singleflight` |
| `internal/generic_sync` | `cloudfso/generic_sync` |
| `internal/mem` | `cloudfso/mem` |
| `internal/net` | `cloudfso/net` |
| `internal/errs` | `cloudfso/errs` |
| `internal/archive/tool` | `cloudfso/archive/tool` |
| `drivers/139` | `drivers/yun139` |
| `sdk/cloudfs` | `sdk/cloudfs` |
| `sdk/cloudfs/cmd/cloudfs` | `cmd/cloudfs` |

如果需要添加新功能（如新的 API 端点），先在 OpenList 源码中找到对应实现，
复制逻辑后改写 import 路径即可。

---

## 8. 扩展新驱动

如果要在本项目中支持更多网盘（不止 139Yun），步骤：

1. 在 `drivers/` 下创建新目录，如 `drivers/aliyun/`
2. 从 OpenList 对应 driver 目录复制 `meta.go / types.go / driver.go / util.go`
3. 改写所有 import 路径：
   - `github.com/OpenListTeam/OpenList/v4/internal/*` → `github.com/milin2436/cloudfs-139/cloudfso/*`
   - `github.com/OpenListTeam/OpenList/v4/drivers/base` → `github.com/milin2436/cloudfs-139/cloudfso/driver`
4. `meta.go` 的 `driver.Config{Name: "..."}` 设置驱动名
5. `init()` 中调用 `op.RegisterDriver()` 注册
6. 运行 `go mod tidy && go build` 验证
