# DEV.md — cloudfs-139 开发者文档

> 这份文档面向**修改者**：说明项目结构、关键实现路径、踩过的坑、构建与测试方式。
> README.md 面向使用者，本文面向维护者。
>
> 要「加一个新功能」先读 [INDEX.md](INDEX.md) —— 全量包索引、扩展点、11 条扩展食谱与缺陷清单。

---

## 1. 项目概要

- **模块名**：`github.com/milin2436/cloudfs-139`
- **Go 版本**：`go.mod` 声明 `go 1.25.0`；当前开发机工具链 **go1.26.5**（`/home/super/bin/go/bin/go`）
  - ⚠️ 早期文档写的 Go 1.27.0 已经不存在了。`cloudfso/cache/weak_cache.go` 用了
    Go 1.27 才有的标准库 `weak` 包，因此用 `//go:build go1.27` 隔离（见 §4.7）
- **来源**：从 [OpenList 4.2.5](https://github.com/OpenListTeam/OpenList) 移植
  - ⚠️ OpenList 源码目录 `/home/super/Downloads/code/OpenList-4.2.5` **已随磁盘损坏丢失**，
    需要对照上游时请重新 clone
  - 移植方式：将 OpenList 的 `internal/` 和 `drivers/base/` 复制到 `cloudfso/`，
    将所有 import 从 `github.com/OpenListTeam/OpenList/v4/...` 改为
    `github.com/milin2436/cloudfs-139/...`
- **用途**：独立的中国移动 139 网盘 SDK + CLI，不依赖 OpenList 主项目

### 一次磁盘损坏与重写（2026-10）

存放原始可构建代码的磁盘损坏。排查结论：仓库只有 1 个 commit（`ad08926`），
没有其它分支、空 stash、`git fsck --unreachable` 也为空，机器上也没有第二份副本 ——
**`sdk/` 和 `cmd/` 完全丢失，无法恢复**。

框架层（`cloudfso/` + `drivers/yun139/`）是完整的，靠 `go build ./cloudfso/...`
验证过可以编译，所以只重写了使用层：

| 丢失内容 | 恢复方式 |
|---------|---------|
| `sdk/cloudfs/{doc,json,config,client,types,userinfo}.go` + `config_test.go` | 按 README + DEV.md 契约重写 |
| `cmd/cloudfs/{main,cmds,util}.go` | 按 README 命令表重写 |
| `GOPROXY` 设置 | 默认 proxy.golang.org 在本机不可达，必须 `export GOPROXY=https://goproxy.cn,direct` |
| 测试配置 `/tmp/139-personal-test.json` | 含真实 token，未恢复；`~/.cloudfs.json` 后来配好了，联调已跑通（见 §5.2） |

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
│   └── cloudfs/            # ★ SDK 层（public 包名 cloudfs）
│       ├── doc.go          # 包文档：挂载路径语义 + 配置示例
│       ├── json.go         # marshalValue/unmarshalValue（防 MarshalJSON 无限递归）
│       ├── config.go       # Config/StorageCfg/JSON、Validate/Load/Marshal/SampleConfig
│       ├── types.go        # File/Space/UserInfo（SDK 自有结构，带小写 json tag）
│       ├── client.go       # ★ Client：init 7 步、路径解析、全部文件操作
│       ├── userinfo.go     # UserInfo：唯一的驱动特化点（接口断言到 yun139）
│       ├── config_test.go  # 配置校验 / JSON 往返 / 路径解析 / addition 缺省 / 路径拼装
│       └── dsn_test.go     # sqliteDSN 回归测试：必须真内存、且跨连接池共享
│
└── cmd/
    └── cloudfs/            # ★ CLI
        ├── main.go         # Cobra 入口，注册 11 个子命令 + -c/--config + -v
        ├── cmds.go         # 子命令实现（ls/get/put/mkdir/rm/mv/cp/rename/space/whoami/init）
        └── util.go         # PrintWriter、newClient、humanSize、atMost、confirm
```

---

## 3. 关键实现路径

### 3.1 Client 初始化流程

入口：`sdk/cloudfs/client.go` → `NewClientFromFile(ctx, path)`

```
NewClientFromFile(ctx, path)
  ├─ LoadConfigFromFile        # 读文件 → 解析 JSON → Validate()
  └─ NewClient(cfg)
      └─ ensureInit(ctx)       # sync.Once；第一个操作时触发，也可显式 c.Init(ctx)
          └─ onceInit(ctx)
              ├─ ① conf.Conf == nil 时设置 {Database:{Type:"sqlite3"}, TempDir:os.TempDir()}
              ├─ ② gorm.Open(sqlite.Open("file:cloudfs?mode=memory&cache=shared&..."))
              │      # ⚠️ mode=memory 是必需的，缺了会写出磁盘文件，见 §4.12
              ├─ ③ db.Init(d)                     # 设全局 gorm + AutoMigrate 8 张表
              ├─ ④ base.InitClient()              # 初始化 resty 客户端
              └─ ⑤ 遍历 storages
                  └─ op.CreateStorage(ctx, model.Storage{MountPath, Driver, Addition})
                      ├─ GetDriver(name)          # 名字不对在这里报 "no driver named: xxx"
                      ├─ db.CreateStorage         # 写入内存 SQLite
                      └─ initStorage              # ⑥ 反序列化 Addition → ⑦ driver.Init(ctx)
```

**驱动注册不在这里**。`client.go` 顶部空导入 `drivers/yun139`，它的 `init()`
调用 `op.RegisterDriver(func() driver.Driver {...})`，名字取自
`driver.Config{Name: "139Yun"}`。本文早期版本写的 `op.RegisterDriver(cfg.Driver,
&driver{})` 是错误签名（`RegisterDriver` 只收一个 constructor，driver 名读自
`Config().Name`），已按实际实现更正。

**注册名与文档拼写的差异**：驱动注册名是 `139Yun`，README 早期写作 `yun139`。
SDK 用 `NormalizeDriverName` 把两种拼写都接受，因此旧配置不用改。

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

三点关键设计：

1. **最长前缀匹配**。同时配了 `/yun139` 和 `/yun139/backup` 时，
   `/yun139/backup/x.tar` 必须归给后者，所以遍历所有 mount 取最长匹配。
   用 `utils.IsSubPath`（带 `/` 分隔符边界）而不是 `strings.HasPrefix`，
   否则 `/yun139X` 会误配到 `/yun139`。
2. **解析只读 config，不读运行时 map**。`resolvePath` / `mountPath` /
   `validateMount` 全部查 `c.cfg.Storages`，唯一的运行时查询点是
   `driverForMount`。这样解析是确定性的、可以在 `Init` 之前单测。
   （早期版本把 `default_mount` 的有效性判断挂在 `op.HasStorage` 上，
   导致框架未启动时静默退回第一个 storage，已被测试抓出来修正。）
3. **`/` 是虚拟根**。`List(ctx, "/")` 不走 `resolvePath`，直接返回配置的
   mount 列表，这样 `cloudfs ls /` 和 README 的示例都成立。
4. **`..` 先被规范化**。`FixAndCleanPath` 内含 `path.Clean`，
   所以 `/yun139/../family` 等价于 `/family` —— 驱动永远看不到 `..`。

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

### 3.5 分享（外链）网关 —— 和 `Type: "share"` 是两套东西

**这是本项目最容易搞混的一点**，所以单独写一节。

| | `Type: "share"` 账号 | `cloudfs share` 创建的分享 |
|---|---|---|
| 方向 | 读取**别人**发来的分享 | **自己**创建分享链接 |
| 网关 | AES 加密分享网关（`sharePost()`） | `yun.139.com/orchestration/personalCloud-rebuild/outlink/v1.0` |
| 传输 | AES-256-CBC 加解密 | **明文 JSON，完全不加密** |
| 账号要求 | `Type: "share"` | 仅 `personal_new` / `personal` |

实现见 `drivers/yun139/share.go`（驱动层）、`sdk/cloudfs/share.go`（SDK）、
`cmd/cloudfs/share.go`（CLI）。三个方法：`CreateOutLink` / `ListOutLinks` /
`DeleteOutLinks`。

**为什么这里不发 `hcy-cool-flag`**：这个头是 `shareHeaders()` 给 AES 分享网关
用的，它会强制服务端把响应改成 AES-256-CBC base64 blob，于是请求体也必须用
32 字节硬编码 key（`UqEZkrjCKfa02pP6jntzFmkzOz86zHUC`，来自前端
`app.ff91d73b.js`）加密。那条路能走通，但纯属多余的移动部件 —— 不带这个头的
同一个 outlink 调用直接返回明文 JSON。所以 `share.go` 里的 outlink 调用刻意
不加这个头。**加上去就等于把简单问题变成密码学问题。**

**签名永远算在明文 body 上**。即使 body 被加密，签名也必须是明文版本，因为
前端在 `da()` 里先 `getNewSign(...e.data...)`、之后拦截器才 `e.data = Dt(e.data)`。

完整契约、验证过程与踩坑见 §4.15。

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

另外注意调用方式：`sqlite.Open(dsn)` 返回的是 `gorm.Dialector`，不是 `*gorm.DB`，
所以必须写 `gorm.Open(sqlite.Open(dsn), &gorm.Config{})` 而不是直接用它。

### 4.7 `weak_cache.go` 用 build tag 隔离

`cloudfso/cache/weak_cache.go` 导入标准库 `weak` 包（`weak.Pointer` / `weak.Make`），
该包 **Go 1.27 才引入**。当前工具链是 go1.26.5，直接编译会报找不到该模块。

处理方式是在文件头加 `//go:build go1.27`，而不是删文件：
`WeakCacheMap` 全仓库 0 个调用方，隔离后无任何行为影响；
将来换回 1.27+ 工具链时它会自动重新参与编译。

### 4.8 复合字面量里 `}` 后面不能换行再接 `,`

```go
// 编译不过：unexpected newline in composite literal
{"ok", &Config{
        Storages: []StorageCfg{{...}},
        DefaultMount: "/yun139"}
     , ""},
```

表驱动测试里很容易写出这种格式（`}` 结束内层字面量、换行后再补逗号）。
**用显式字段名就绕开了**，也更可读：

```go
{
    name:    "ok",
    cfg:     &Config{Storages: []StorageCfg{{...}}, DefaultMount: "/yun139"},
    wantErr: "",
},
```

### 4.9 `model.Object` 没有 json tag

`cloudfso/model.Object` 的字段全部无 tag，直接 `json.Marshal` 会输出
`{"Name":...,"Size":...,"Modified":...}`（大写字段名）。
所以 SDK 在 `types.go` 里自己定义了 `File` / `Space` / `UserInfo` 并带小写 tag，
`toFile()` 做映射 —— **不要图省事直接返回 `model.Obj`**。

### 4.10 上传必须自己构造 `stream.FileStream`

`stream.FileStream` 没有公开的构造器，`size` 字段是私有的。
`GetSize()` 的逻辑是 `if f.size > 0 { return f.size }`，否则回退到嵌入的
`Obj.GetSize()`。因此传文件大小只能走嵌入的 `model.Object`：

```go
file := &stream.FileStream{
    Ctx:      ctx,
    Obj:      &model.Object{Name: st.Name(), Size: st.Size(), Modified: st.ModTime(), Ctime: st.ModTime()},
    Reader:   f,
    Mimetype: utils.GetMimeType(st.Name()),
}
return op.Put(ctx, d, rel, file, up)
```

`*model.Object` 满足 `model.Obj` 接口（8 个方法），但不满足 `FileStreamer`，
这没问题 —— 因为 `Obj` 接口本身不要求 `FileStreamer`。

### 4.11 ⚠️ `.gitignore` 的裸 `cloudfs` 会把源码目录整个排除

这是本次磁盘损坏之外最值得记的一条。`.gitignore` 原来的编译产物规则是：

```
# 编译产物
cloudfs
```

gitignore 里**不带 `/` 的模式匹配任意层级的同名文件*和目录***。所以这条规则
除了忽略根目录那个 27M 的二进制，还把源码目录 `sdk/cloudfs/` 和 `cmd/cloudfs/`
**整个排除在版本控制之外**：

```bash
$ git check-ignore -v sdk/cloudfs/client.go
.gitignore:2:cloudfs	sdk/cloudfs/client.go     # 被忽略！

$ git status --porcelain
 M DEV.md                          # sdk/ 和 cmd/ 压根不会出现在列表里
```

`git status` 连 `?? sdk/` 都不会显示，所以这个问题极其隐蔽 ——
**这很可能就是原来那份「可构建代码」从没进过 git 的原因。**

修法是把模式锚定到仓库根：

```
/cloudfs          # 只匹配根目录的二进制
/cloudfs.exe
```

副作用：在子目录里编译出的同名二进制不再被忽略。因此约定
`go build -o /tmp/cloudfs ./cmd/cloudfs/`（写到 /tmp，不要写进仓库）。
`*.exe` / `*.test` / `*.out` 仍然全局生效。

**排查手法**：改完 `.gitignore` 后用 `git check-ignore -v <path>` 逐条验证，
不要只信 `git status` —— 被忽略的文件在那里是完全不可见的。

### 4.12 ⚠️ `file::memory:...` **不是**内存数据库，它会在 cwd 写出磁盘文件

重写时发现的最隐蔽的一个。原 DSN 是这样的：

```go
const sqliteDSN = "file::memory:cloudfs?cache=shared&_pragma=busy_timeout(5000)"
```

注释写的是"内存数据库"，读起来也很像内存数据库。但它**会在进程当前目录创建一个
真实文件，文件名就叫 `:memory:cloudfs`**：

```bash
$ ls -la
-rw-r--r-- 1 super super 65536 Oct 12 02:08 :memory:cloudfs   # ← 仓库根目录里！
```

原因：SQLite 只把**精确等于** `:memory:` 的这个名字当作匿名内存库，其它任何名字
一律按普通文件名处理。`file::memory:cloudfs` 的路径部分是 `:memory:cloudfs`，
不等于 `:memory:`，于是成了文件名。

两个后果，第二个更麻烦：

1. **凭据泄露到任意 cwd**。`storages.addition` 这一列存的是 Addition JSON，里面
   带 139 的 `Authorization` token（配置里给了 `Username/Password` 时也带密码）。
   它在哪个目录运行 CLI，就在哪个目录落一个未加密的 sqlite 文件。
2. **第二次运行直接崩**。文件跨进程留存，第二次 `cloudfs ls` 在同一个目录跑时，
   `CreateStorage` 插入同一个 `mount_path` 撞上唯一约束：

   ```
   error: storage "/yun139" (yun139): failed create storage in database:
    constraint failed: UNIQUE constraint failed: storages.mount_path (2067)
   ```

   注意这个报错发生在**驱动初始化之前**，所以问题看起来像是配置重复，
   完全指向不了数据库。

**正确写法**：

```go
const sqliteDSN = "file:cloudfs?mode=memory&cache=shared&_pragma=busy_timeout(5000)"
```

`mode=memory` + `cache=shared` 才是"有名字但仍在内存里、且多连接共享"的组合。
`_pragma=` 这个参数 modernc.org/sqlite 是支持的（mattn 的驱动也支持）。

**顺带一个相反的坑**：裸 `:memory:` 也不要随便用。它对 database/sql 的**每个
连接各建一个私有库**，gorm 连接池里开第二个连接就看不到第一个建好的表
（`no like table: storages`）。实测对比：

| DSN | 跨连接池共享 | cwd 落文件 |
|-----|:---:|:---:|
| `file::memory:cloudfs?cache=shared&...` | ✅ | ❌ **会落** |
| `file:cloudfs?mode=memory&cache=shared&...` | ✅ | ✅ 不落 |
| `:memory:` | ❌ 每连接一个库 | ✅ 不落 |

回归测试 `TestSQLiteDSNIsInMemoryAndShared`（`sdk/cloudfs/dsn_test.go`）锁死了这两点：
断言第二个连接池能看到第一个插入的行，并断言运行前后当前目录的文件列表不变。
把 DSN 改回 `file::memory:cloudfs` 它会立刻失败并点名漏出来的文件名。

**教训**：URI 型 DSN 的"看起来像 X"不等于"是 X"。任何带 `file:` 前缀的 DSN
都要实测一次——开库、插一行、从另一个连接池读回来、再看一眼 cwd。

### 4.13 ⚠️ 结构体 tag 里的 `default:"..."` **不会**被 JSON 反序列化应用

`drivers/yun139/meta.go` 的 Addition 里写着：

```go
Type string `json:"type" type:"select" options:"personal_new,family,group,personal,share" default:"personal_new"`
```

`default:"personal_new"` 只是给 OpenList **Web 表单渲染器**看的提示。
手写在配置文件里的 Addition 如果不写 `"Type"`，`utils.Json.Unmarshal` 之后
`Addition.Type` 就是空串，`driver.Init` 的 `switch d.Addition.Type` 直接掉进
`default` 返回 `errs.NotImplement`，用户看到一句毫无指向的 `not implement`：

```
error: storage "/yun139" (yun139): failed init storage ...: not implement
```

SDK 在 `client.go` 的 `normalizeAddition` 里补上这个缺省，并且**只在值缺失或
为空时补**——拼错（比如 `personla_new`）不会被悄悄纠正，仍然会报 NotImplement。
回归测试 `TestNormalizeAddition`。

同类陷阱：`driver.Config` / `Addition` 的 tag 里所有非标准库字段
（`type:`、`options:`、`default:`、`secret:`、`required:`、`help:`）
都只是 Web UI 元数据，Go 代码里没有任何机制会自动生效。

### 4.14 ⚠️ `op.List` 返回的对象 **没有** Path

`op.List` 和 yun139 的 driver 都不会给列表结果填 `model.Object.Path`
（只有合成的根目录对象会填 `Path: "/"`）。所以图省事写

```go
Path: joinPath(mount, o.GetPath())   // 恒等于 mount！
```

会得到所有条目的 `path` 都塌缩成挂载点：

```json
[{"name":"x02.hdy","path":"/yun139"}, {"name":"001.hdy","path":"/yun139"}]
```

列表结果里只有 `Name` 可用，全路径必须由调用方自己拼：
列出 `rel` 目录时，子项路径是 `mount + "/" + rel + "/" + name`。
SDK 里对应 `childPath()` + `toFile(mount, rel, o)`，回归测试 `TestToFilePaths`。

### 4.15 ⚠️ 分享接口：`commonAccountInfo` 必须放在 action 对象**里面**

加分享功能时踩的坑，全部来自 outlink 网关，一次都不会在编译期报错，只有真机
调用才暴露。

**网关与路径**：`https://yun.139.com/orchestration/personalCloud-rebuild/outlink/v1.0`

复用驱动已有的 `newRequest()`（mcloud-sign 那套）打到一个不同的 host 即可，
不需要新客户端。三个接口：

| 操作 | 路径 | body 的 key |
|---|---|---|
| 创建 | `/getOutLink` | `getOutLinkReq` |
| 列表 | `/getOutLinkList` | `getOutLinkLstReq` |
| 撤销 | `/delOutLink` | `delOutLinkReq` |

**坑一：`commonAccountInfo` 要嵌在 action 对象内部。** 放顶层会得到
`1010010015 鉴权失效`。正确形状：

```json
{"getOutLinkReq": {"commonAccountInfo": {"account": "17144995673", "accountType": 1}, "caIDLst": [...]}}
```

这里也是唯一用**手机号**而不是 Cookie Authorization 标识账号的地方。
（前端代码本身并不发这个字段 —— 是 axios 拦截器从数组 `sa` 注入的，见
`app.js:149056`，所以从前端源码抄 body 一定会漏。）

**坑二：body 的 key 和路径不拼写一致。** `getOutLinkLstReq`（`Lst`）对应
`/getOutLinkList`（`List`）。照抄一个就会 404。

**坑三：`qryType` 没有默认值。** 列表接口 `qryType: 1` = 我发出的分享，`2` =
别人发给我的；传 `0` 会得到 `1010000500`。所以 SDK 层强制要求调用方二选一，
不替它猜。列表还必须有 `srt: 0, srtDr: 0`，漏了会得到"外链的排序方式不能为空"。

**坑四：139 的数字字段类型不稳定。** 同一个逻辑数字在有的接口返回 `123`、
有的返回 `"123"`（列表接口的 `count` 就是字符串）。代码里用 `jsonInt64`
自定义 `UnmarshalJSON` 同时吃两种，解析失败时**留零值而不是让整个分页失败**。

**坑五：不要给 CLI 暴露"自定义提取码"。** 前端创建分享的请求里只有 `encrypt`
标志，没有码字段 —— 码是服务端生成的。第一版 SDK 按直觉写了
`ShareOptions.Password string`，结果它永远不传输，纯属误导 API 的用户。
后来改成 `Encrypted bool`，和实际行为一致。

**坑六：`openCount` 不是"有效分享数"。** 一条未过期、可用、未失效的分享，
`openCount` 返回 `0`。字段照实暴露了，但注释里写明它不可信，CLI 表格也不再用
它当"active"显示。

**校验过的服务端错误码**（用来判断是账号问题还是代码问题）：

| 现象 | 码 / 信息 | 含义 |
|---|---|---|
| 文件 ID 不存在 | `200000400` 用户没有权限操作文件/目录记录/记录已被删除 | 正常：说明请求真的到了校验环节 |
| ID 列表为空 | `9146` 非法参数为空 | 参数问题 |
| `pubType: 2` 但没 viewer | 好友分享时，好友列表不能为空 | 参数问题 |
| 链接 ID 不存在 | 外链不存在/外链被分享者取消 | 撤销接口正常报错 |
| 分享名含敏感词 | `9470` | 服务端内容审核 |
| 家庭云/群组云调分享 | `sharing is not supported for account type ...` | 驱动层本地拒绝，没发请求 |

### 4.16 探针的取舍：能删就删

`drivers/yun139/share_probe_test.go`（`//go:build probe`）在**调研阶段**用：
把网关的 header 列表、签名、body 全复刻一份，用来把上表的错误码一个一个
钉死。**契约钉死之后它就删掉了**，原因是它把同一套 header/body 契约抄了一遍，
必然和 `share.go` 的真实实现漂移 —— 而且它比生产代码更容易看起来"还在工作"，
其实测的是旧的假设。

如果之后要再加一个类似的功能，调研阶段照写探针没问题，但**功能落地后记得删**，
真机验证直接跑 CLI 就好（§5.2）。别为了留个"冒烟测试"而留一份平行实现。

---

## 5. 构建与测试

### 5.1 构建

```bash
cd /home/super/gowork/cloudfs-139

# ⚠️ 必须先设：默认的 proxy.golang.org 在本机不可达，会一直 i/o timeout
export GOPROXY=https://goproxy.cn,direct

go mod tidy              # 应该无输出、不改任何文件
go build ./...           # 框架 + SDK + CLI 全量编译
go vet ./...
go test ./...            # 见 5.3
go build -o /tmp/cloudfs ./cmd/cloudfs/
```

### 5.2 端到端测试

用真实的 `~/.cloudfs.json`（含 Authorization）可以直接跑：

```bash
go build -o /tmp/cloudfs ./cmd/cloudfs/
/tmp/cloudfs whoami                 # 默认读 ~/.cloudfs.json，不带 -c
/tmp/cloudfs ls /                   # 虚拟根，列出所有挂载点
/tmp/cloudfs ls /yun139             # 挂载点内容
/tmp/cloudfs -v ls /yun139          # -v 看驱动层 debug 日志
```

已验证通过的操作（真实账号）：`whoami`、`ls`、`mkdir`、`mv`、`rename`、`cp`、
`rm`、`Stat`、`Download`，以及分享全套：`share`（无码永久 / 加码 1 天，各一次）、
`shares`（发出 + 收到）、`unshare`（含确认交互与 `-f`）。分享的验证走的是
自建空目录 `cloudfs-share-probe` → 分享 → 撤销 → 删除，全程只暴露一个空目录。

**分享是唯一有外部影响的命令。** 生成出来的 URL 任何拿到它的人都能下载文件，
所以测试时只拿自建目录做实验，创建后**立刻 `unshare`**，别留着。README 里也
把这点写进了命令表格。

三个坑，都是真机才暴露的：

- **`addition.type` 不填会报 `not implement`**。见 §4.13，SDK 已补缺省。
- **上传可能报 `00010012 资源配额不足`**。这是账号存储满了，不是代码问题。
  判断方法：`-v` 看 `/hcy/file/create` 的请求体，`size` / `contentHash` /
  `parentFileId` 都对就只是配额。
- **`space` 必须配 `user_domain_id`**（Cookie 里的 `ud_id`），否则驱动返回
  `not implement`。SDK 已转成可操作报错。

`-v` 会把 Authorization token 以明文打进 debug 日志（驱动自己打的，
`storage &{... Addition:{Authorization:*** 排查完关掉 `-v`，
不要把带 token 的日志提交或贴出来。

测试写路径时**先 `mkdir` 一个自己的目录**，在里面做 `mv`/`rename`/`cp`/`rm`，
跑完 `rm -f` 清掉，不要拿账号里已有的文件做实验。

### 5.3 测试

```bash
go test ./sdk/...          # SDK 配置校验 / JSON 往返 / 路径解析 / DSN 回归，13 个用例
go test -race ./sdk/...    # 并发安全
go test -v -run TestResolvePath ./sdk/...   # 单个用例
```

路径解析、配置校验、JSON 往返都可以离线测（不触发 `ensureInit`）。
需要真实 token 的只有驱动初始化之后的操作；用空 token 跑一次可以确认
整条启动链路是通的，报错应停在驱动自己的参数校验：

```bash
/tmp/cloudfs -c <空 token 配置> ls /
# → error: storage "/yun139" (yun139): ... authorization is empty and username/password is not provided
```

### 5.4 调试模式

`ls --output raw` 会捕获 HTTP 请求/响应（通过 `base.EnableHTTPCapture()`），
输出原始 HTTP 交换内容，方便排查 API 问题。`ls` 还支持 `--output json`。
抓包必须在请求发起前开启，SDK 里对应的三个函数是
`cloudfs.EnableHTTPCapture` / `DisableHTTPCapture` / `FormatHTTPCaptures`。

---

## 6. 环境变量与开发机

| 项 | 值 |
|------|----|
| GOROOT | `/home/super/bin/go`（`go` 在 `/home/super/bin/go/bin/go`） |
| Go 版本 | **go1.26.5**（早期文档写的 1.27.0 已不存在） |
| GOPROXY | **必须** `https://goproxy.cn,direct`；默认的 proxy.golang.org 在本机 i/o timeout |
| OS | Linux amd64 |
| CGO | 无 gcc，SQLite 只能用纯 Go 的 glebarez 版本 |
| 原始 OpenList | ❌ 已丢失（`/home/super/Downloads/code/OpenList-4.2.5`），需重新 clone |
| 测试配置 | ✅ `~/.cloudfs.json`（账号 `17144995673`，个人网盘）；旧的 `/tmp/139-personal-test.json` 已丢失 |

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
