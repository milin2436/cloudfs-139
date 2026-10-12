# INDEX.md — cloudfs-139 代码索引（面向「加功能」）

> 本文是**代码地图**：给"要在这个仓库里加一个功能"的人用的。
> 使用者文档见 [README.md](README.md)，移植与踩坑史见 [DEV.md](DEV.md)。
> 全文基于 2026-10-12 的 `main`，共 131 个 Go 文件。使用层（`sdk/` + `cmd/`）为
> 2026-10-12 重建 —— 原代码随磁盘损坏丢失，见 §0 与 DEV.md §1。
> 2026-10-12 另加分享（外链）功能三层实现，见 §6.9 与 DEV.md §3.5 / §4.15。
> 同日把 `space` 从"必须先有 `user_domain_id`"改成按云盘类型分项显示，
> 并钉死配额 API 契约，见 §6.10 与 DEV.md §3.6 / §4.17。
> 所有引用格式为 `文件:行号`，可直接跳转。

---

## 0. 先读这一段：仓库的真实状态与文档落差

**2026-10-12 更新：使用层已经补上了。** 原始可构建代码所在的磁盘损坏，`sdk/` 和 `cmd/` 全部丢失且无法恢复（详见 DEV.md §1），已按 README/DEV.md 契约重写完成：

| 曾经缺失 | 现在 |
|---|---|
| `sdk/cloudfs/`（SDK） | ✅ `doc.go` `json.go` `config.go` `types.go` `client.go` `userinfo.go` `quota.go` `share.go` + `config_test.go` `dsn_test.go` |
| `cmd/cloudfs/`（CLI，14 个子命令） | ✅ `main.go` `cmds.go` `quota.go` `share.go` `util.go` |
| `db.Init` / `base.InitClient` 无调用方 | ✅ 由 `Client.onceInit` 调用 |
| `cloudfso/op` 零调用方 | ✅ 全部 op 方法都有入口（除归档相关） |
| `*_test.go` 数量为 0 | ✅ `config_test.go` 12 个 + `dsn_test.go` 1 个，共 13 个用例 |
| `weak_cache.go` 编译阻塞 | ✅ `//go:build go1.27` 隔离 |
| 重写时引入的 `sqliteDSN` bug（`file::memory:...` 会在 cwd 落磁盘文件并跨进程撞唯一约束） | ✅ 改为 `file:cloudfs?mode=memory&cache=shared`，`dsn_test.go` 回归锁死。见 DEV.md §4.12 |

`go build ./...` / `go vet ./...` / `go test -race ./sdk/...` 全部通过。

### 仍然成立的落差（改动前必看）

1. **真机联调已做（2026-10-12）**，账号 `17144995673`。已跑通并验证：
   `whoami`、`ls`（含嵌套目录与虚拟根）、`mkdir`、`mv`、`rename`、`cp`、`rm`
   （含确认交互）、`Stat`、`Download`（前 256KiB，验证 link 解析与流式管道）、
   **分享全套**（`share` 无码永久 / 加码 1 天各一次 → `shares` 列出 → `unshare` 撤销，
   见 §6.9）。
   **上传被服务端以 `00010012 资源配额不足` 拒绝** —— 账号存储已满，
   请求体本身正确（`size`/`contentHash`/`parentFileId` 全部匹配本地文件），
   所以是账号状态而非代码问题。**已定位到具体数字**：20 GiB 配额，
   个人云 196,894 MB + 家庭云 26,079 MB = 222,973 MB 已用，超额约 198 GiB
   （见 §6.10）。`space` 已修复为不依赖 `user_domain_id`，实测可用。
   ⚠️ 因为上传不可用，分享的真机验证只能拿**空目录**做（新建 → 分享 → 撤销 → 删除），
   还没验证过"分享一个真实文件"和"分享一个非空目录"。
2. **归档功能全部返回 `UnknownArchiveFormat`**。`archive/tool.RegisterTool` 全仓库
   0 个调用方，zip/rar/7z 解压不可用。
3. **5 张 OpenList 多租户表是无用残留**（见 §9）。
4. §8 的 22 条缺陷仍在，其中 3 条会影响并发与稳定性（`util.go:1167` 全局重定向策略、
   `util.go:914` 的 `panic(err)`、yun139 无锁）。

### 因此"加功能"的优先级排序

1. **P0 端到端联调 — 已完成**（2026-10-12），见上表第 1 条。重写的使用层与驱动协议匹配。
   唯一没跑通的是上传，卡在账号配额，不是代码。
2. **P1 — 修并发缺陷**：先修 §8 的 `panic(err)`（可以整进程崩掉）。
3. **P2 — 业务能力扩展**：搜索、回收站、批处理、离线任务等，按 §10 食谱做。

---

## 1. 架构与调用链

```
┌────────────────────────────────────────────────────────────┐
│  使用层  cmd/cloudfs（CLI） / sdk/cloudfs（SDK）           │  ← 已重建
├────────────────────────────────────────────────────────────┤
│  op  op.List / Get / Link / Put / MakeDir / Remove /       │
│      Move / Rename / Copy / Other / GetStorageDetails       │  ← 唯一入口层
│      path.go: GetStorageAndActualPath(rawPath) → (driver, rel)│
├────────────────────────────────────────────────────────────┤
│  driver  driver.Driver = Meta + Reader（接口契约）          │  ← 契约层
├────────────────────────────────────────────────────────────┤
│  drivers/yun139  Yun139（1174+1771+465+403+37 行）        │  ← 自研核心
│      5 种账号类型 × 9 个操作 × 10 个 HTTP 通道              │
├────────────────────────────────────────────────────────────┤
│  base（resty） net（HTTP 服务/分片下载器） stream（流抽象）  │
│  model（领域模型） db（GORM+SQLite） cache singleflight     │
│  conf errs utils buffer hybrid_cache http_range cron mem    │
└────────────────────────────────────────────────────────────┘
                    yun.139.com / share-kd-njs.yun.139.com
```

**三条最关键的调用约定**（写任何新功能都会碰到）：

1. **op 层不认路径，只认 driver 实例。** 所有 op 文件操作第一个业务参数是 `storage driver.Driver`。调用方必须先 `op.GetStorageAndActualPath(rawPath)` 拿到 `(driver, actualPath)`，op 不做二次路由。→ `cloudfso/op/path.go:16`
2. **驱动能力靠类型断言发现，不靠注册。** 在 `cloudfso/driver/driver.go` 里定义一个 `interface`，驱动只要实现了同名方法，op 层的 `switch s := storage.(type)` 就会自动生效，**不需要任何注册步骤**。→ 范例 `op/fs.go:288`、`op/storage.go:472`
3. **所有错误判定用哨兵 + `pkgerr.Cause` 解包，不要用字符串比较。** → `cloudfso/errs/`

---

## 2. 包索引总表

| 包 | 行数量级 | 职责 | 139 场景是否真用 |
|---|---|---|---|
| `sdk/cloudfs` | 1743 | 使用层 SDK：配置、路径解析、`Client.*` 包装（含 `share.go` 217 行分享、`quota.go` 96 行配额） | ★★★ |
| `cmd/cloudfs` | 1053 | CLI：14 个子命令（含 `share.go` 302 行分享三件套、`quota.go` 191 行配额表格） | ★★★ |
| `drivers/yun139` | 3850 | 139 云盘驱动，5 账号类型 | ★★★ 核心 |
| `cloudfso/op` | ~4100 | 操作层 facade，SDK/CLI 唯一入口 | ★★★ |
| `cloudfso/driver` | ~430 | Driver 接口契约 + Config + 进度/限速 | ★★★ |
| `cloudfso/model` | ~3500 | Obj / Link / FileStreamer / Storage / User 等领域模型 | ★★★ |
| `cloudfso/conf` | ~535 | 进程级单例 `conf.Conf`、设置项 key 常量、context key | ★★★（必须最先初始化） |
| `cloudfso/base` | ~600 | resty 客户端封装、HTTP 抓包、上传进度缓存 | ★★★ |
| `cloudfso/errs` | ~90 | 哨兵错误 | ★★★ |
| `cloudfso/utils`(+random) | ~4000 | 路径/哈希/io/闭包/重试 | ★★★ |
| `cloudfso/stream` | ~1830 | FileStream / SeekableStream / 限速 | ★★★（上传下载必经） |
| `cloudfso/net` | ~2550 | 服务端 Range 响应、并发分片下载器、OSS | ★★（下载/中转用） |
| `cloudfso/hybrid_cache` | ~1400 | 内存+文件双层块缓存 | ★★（大文件缓存） |
| `cloudfso/buffer` | ~600 | `Block` 抽象、PipeBuffer | ★★ |
| `cloudfso/http_range` | ~180 | RFC7233 Range 解析 | ★★ |
| `cloudfso/db` | ~1900 | GORM 8 张表 CRUD | ★ 仅 `storage` 表真用 |
| `cloudfso/cache` | ~900 | TTL 缓存（Keyed/Typed/Weak） | ★★（`op.Cache` 依赖） |
| `cloudfso/singleflight` | ~450 | 泛型请求合并 | ★★★（op 大量用） |
| `cloudfso/generic_sync` | ~1190 | `sync.Map` 泛型版 | ★★ |
| `cloudfso/mem` | ~650 | 大块内存预留（mmap/VirtualAlloc） | ★（混合缓存依赖） |
| `cloudfso/cron` | ~60 | 极简定时器 | ★★★（12h token 刷新） |
| `cloudfso/archive/tool` | ~220 | 压缩包工具注册表 | ✖ **死代码**（见 §8） |

---

## 3. 契约层：`cloudfso/driver`

`driver.go:9` `type Driver interface { Meta; Reader }` —— 写操作与扩展能力**全部**通过独立小接口 + 类型断言表达，聚合接口只有 4 个必选方法。

### 3.1 必选（不实现就编译不过）

| 方法 | 定义行 | 说明 |
|---|---|---|
| `Config() driver.Config` | `driver.go:16` | 声明能力（Name / NoCache / CheckStatus / ProxyRangeOption / LocalSort …） |
| `GetStorage() *model.Storage` | `driver.go:16` | **有默认实现**：内嵌 `model.Storage` 即自动满足 |
| `SetStorage(model.Storage)` | `driver.go:16` | 同上（`model/storage.go:45`） |
| `GetAddition() driver.Additional` | `driver.go:16` | 返回附加配置**指针**，供 JSON unmarshal |
| `Init(ctx) error` / `Drop(ctx) error` | `driver.go:16` | 幂等要求：已初始化需先 Drop |
| `List(ctx, dir model.Obj, args model.ListArgs) ([]model.Obj, error)` | `driver.go:32` | ID 语义强约束见下方 |
| `Link(ctx, file model.Obj, args model.LinkArgs) (*model.Link, error)` | `driver.go:32` | 返回 URL / RangeReader 二选一 |

> **`List` 的 ID 语义**（`driver.go:33-36` 注释）：按**路径**识别的驱动，子对象 `ID` 必须是 `path.Join(dir.GetID(), obj.GetName())`；按 **id** 识别的驱动，ID 用真实 id。139 用的是真实 id。

### 3.2 可选接口（24 个，`driver.go` 全文 220 行）

| 接口 | 行 | 方法 | yun139 是否实现 |
|---|---|---|---|
| `Other` | 28 | `Other(ctx, model.OtherArgs) (interface{}, error)` | ✅ |
| `GetRooter` | 41 | `GetRoot(ctx) (model.Obj, error)` | ❌（靠 `IRootId`） |
| `Getter` | 45 | `Get(ctx, path) (model.Obj, error)` | ✅ **仅 share**，其余 `NotImplement` |
| `Mkdir` / `MkdirResult` | 59 / 113 | `MakeDir(...) error` / `(Obj, error)` | ✅ Mkdir（无 Result） |
| `Move` / `MoveResult` | 63 / 117 | `Move(...) error` / `(Obj, error)` | ✅ **MoveResult** |
| `Rename` / `RenameResult` | 67 / 121 | `Rename(...) error` / `(Obj, error)` | ✅ Rename |
| `Copy` / `CopyResult` | 71 / 125 | `Copy(...) error` / `(Obj, error)` | ✅ Copy |
| `Remove` | 75 | `Remove(ctx, obj) error` | ✅ |
| `Put` / `PutResult` | 79 / 129 | `Put(ctx, dst, FileStreamer, UpdateProgress) error` / `(Obj,error)` | ✅ Put |
| `PutURL` / `PutURLResult` | 106 / 156 | `PutURL(ctx, dst, name, url) ...` | ❌ |
| `ArchiveReader` | 163 | `GetArchiveMeta` / `ListArchive` / `Extract` | ❌ |
| `ArchiveGetter` | 179 | `ArchiveGet` | ❌ |
| `ArchiveDecompress(Result)` | 186 / 190 | 解压 | ❌ |
| `WithDetails` | 199 | `GetDetails(ctx) (*model.StorageDetails, error)` | ✅ |
| `Reference` | 204 | `InitReference(storage Driver) error` | ✅（多挂载点共享登录态） |
| `LinkCacheModeResolver` | 208 | `ResolveLinkCacheMode(path) LinkCacheMode` | ❌ |
| `DirectUploader` | 213 | `GetDirectUploadTools` / `GetDirectUploadInfo` | ❌ |

**扩展新能力的标准动作**：在第 3 节列的 24 个接口之外新增一个 → 写进 `cloudfso/driver/driver.go` → 驱动加同名方法 → 在 `op/fs.go` 加一个 facade 函数（照抄 §10 食谱 1）。**不需要注册。**

### 3.3 `driver.Config`（`config.go:3`）

18 个字段控制 driver 全局行为。对"加功能"最重要的是：

- `Name`：`driverMap` 的 key（yun139 = `"139Yun"`）
- `NoCache` / `CheckStatus`：op 层是否缓存、是否校验 Status
- `OnlyProxy` / `NoLinkURL` → `MustProxy()`；`PreferProxy` → `DefaultProxy()`
- `LinkCacheMode`：`LinkCacheAuto(-1)` / `LinkCacheNone(0)` / `LinkCacheIP(1<<0)` / `LinkCacheUA(1<<1)`，**位标志可 OR 组合**
- `Config.CheckStatus` / `NoOverwriteUpload` / `ProxyRangeOption` / `NoLinkURL` / `LinkCacheMode` 的 json tag 是 `"-"`，**不出现在配置表单**，只能代码内静态设置。

其余：`driver.Item`/`driver.Info`（表单元数据，`item.go`）、`driver.RootPath`/`driver.RootID`（可嵌入的根目录定位，yun139 用 `RootID`）、`driver.NewProgress` + `driver.Progress`（`io.TeeReader` 上报进度）、`driver.NewLimitedUploadStream/File` + `driver.ServerUploadLimitWaitN`（上传限速，`utils.go:39-55`）。

---

## 4. 领域模型：`cloudfso/model`

### 4.1 `Obj` 接口（`obj.go:22`）— 全项目对象契约

```go
Obj = GetSize() int64  GetName() string  ModTime() time.Time  CreateTime() time.Time
    IsDir() bool  GetHash() utils.HashInfo  GetID() string  GetPath() string
```

**关键**：`GetID()` / `GetPath()` 的注释明确写着 *"The internal information of the driver"* —— 它们的含义由驱动决定（路径 or 真实 id），外部代码不能假设。

默认实现 `model.Object`（`object.go:22`）是**驱动构造对象的主力**：`{ID, Path, Name string; Size int64; Modified, Ctime time.Time; IsFolder bool; HashInfo; Mask ObjMask}`。
⚠️ **`Object` 全部字段没有 json tag**，序列化出来是大写字段名 `{"ID":...,"Name":...}`，做 CLI/HTTP 输出时必须自行映射。

### 4.2 对象可选属性 = 包装器模式

需要给对象附加属性（缩略图/直链/提供者/掩码/存储详情）**不要改 `Obj` 接口**，照抄 `object.go` 末尾的 `ObjThumb` / `ObjectURL` / `ObjectProvider` / `ObjWrapMask` 写组合类型。
包装器**必须实现 `Unwrap() Obj`**（`obj.go:18`），否则 `GetThumb`/`GetUrl`/`GetObjMask`/`GetStorageDetails`（`obj.go:152/165/178`）的链式穿透全部失效。

### 4.3 能力掩码 `ObjMask`（`obj.go:239`）

`Virtual / NoRename / NoRemove / NoMove / NoCopy / NoWrite / Temp` 七位。
组合常量：`Locked = NoRename|NoRemove|NoMove`，`ReadOnly = Locked|NoWrite`。
查询：`model.GetObjMask(obj)`（0 = 无限制）、`model.ObjHasMask(obj, m)`。
op 层在 Move/Rename/Copy/Put 前都会检查这些位 → 返回 `errs.PermissionDenied`。

### 4.4 `FileStreamer`（`obj.go:37`）— 上传侧契约

`Obj` + `io.Reader` + `ClosersIF` + `GetMimetype/NeedStore/IsForceStreamUpload/GetExist/SetExist` + `RangeRead` + `CacheFullAndWriter` + `GetFile`。
⚠️ `RangeRead` 与 `CacheFullAndWriter` 的注释都强调：**"for a non-seekable Stream, if Read is called, this function won't work"** —— 一旦读过流，这两个方法失效。

### 4.5 `Link`（`args.go:28`）— 下载契约

`{URL string; Header; RangeReader RangeReaderIF; Expiration *time.Duration; Concurrency, PartSize int; ContentLength int64; SyncClosers; RequireReference bool}`

- 三选一：`URL`（最常见）/ `RangeReader`（"recommended way if can't use URL"）/ 本地路径
- `Expiration` = **本地缓存时长**，不是远端 URL 过期
- `RequireReference=true` 时，`SyncClosers` 关闭后 Link 即失效（op 层据此用 `SetTypeWithExpirable`）
- `(*Link).Clone()`（`args.go:45`）是深拷贝

### 4.6 参数结构族（`args.go`，按动作命名）

`ListArgs{ReqPath, S3ShowPlaceholder, Refresh, WithStorageDetails, SkipHook}`（:13）、`LinkArgs{IP, Header, Type, Redirect}`（:21）、`Archive*Args`（:71-96，层层内嵌）、`Sharing*Args`（:98-116）、`RangeReaderIF/RangeReadCloser`（:118-129）。

> ⚠️ **文档常见错误命名**：不存在 `model.Args`、`model.SearchArgs`、`model.Req`、`model.Resp`。`req.go` 只有 `PageReq{Page,PerPage}` + `Validate()`（静默修正，不返回错误）。各驱动的请求/响应体定义在**各自的 `types.go`**（如 `drivers/yun139/types.go` 的 `BaseResp`）。

### 4.7 gorm 实体（表结构）

`Storage`（storage.go:8）、`Meta`（meta.go:3）、`User`（user.go:45）、`SettingItem`（setting.go:25）、`SearchNode`（search.go:23）、`TaskItem`（task.go:3）、`SSHPublicKey`（sshkey.go:11）、`SharingDB`（sharing.go:13）。详见 §9。

---

## 5. 操作层：`cloudfso/op`（未来 SDK 的唯一入口）

### 5.1 导出 API

| 类别 | 符号 | 位置 |
|---|---|---|
| **路径路由** | `GetStorageAndActualPath(rawPath) (driver, actualPath, error)` | `path.go:16` |
| **驱动注册** | `RegisterDriver` / `GetDriver` / `GetDriverNames` / `GetDriverInfoMap` | `driver.go:18/26/34/42` |
| **storage 管理** | `GetAllStorages` / `HasStorage` / `GetStorageByMountPath` / `GetBalancedStorage` / `CreateStorage` / `LoadStorage` / `EnableStorage` / `DisableStorage` / `UpdateStorage` / `DeleteStorageById` / `MustSaveDriverStorage` | `storage.go:29-289` |
| **空间** | `GetStorageDetails(ctx, storage, refresh...)` | `storage.go:468` |
| **文件** | `List` / `Get` / `GetUnwrap` / `Link` / `Other` / `MakeDir` / `Move` / `Rename` / `Copy` / `Remove` / `Put` / `PutURL` | `fs.go:26-782` |
| **压缩包** | `GetArchiveMeta` / `GetArchiveToolAndStream` / `ListArchive` / `ArchiveGet` / `DriverExtract` / `InternalExtract` / `ArchiveDecompress` | `archive.go` |
| **递归扫描** | `RecursivelyList` / `RecursivelyListStorage` / `BeginManualScan` / `StopManualScan` / `ManualScanRunning` | `recursive_list.go` |
| **缓存** | `Cache`（全局 `CacheManager`）：`Key` / `DeleteDirectoryTree` / `DeleteDirectory` / `SetUser/GetUser/DeleteUser` / `SetSetting*` / `SetStorageDetails/GetStorageDetails/InvalidateStorageDetails` / `ClearAll` | `cache.go:35-148` |
| **Hook** | `RegisterObjsUpdateHook` / `HandleObjsUpdateHook` / `RegisterSettingItemHook` / `HandleSettingItemHook` / `RegisterStorageHook` | `hook.go:25/29/101/105/123` |
| **设置/用户/分享/meta/sshkey** | `op/setting.go` `op/user.go` `op/sharing.go` `op/meta.go` `op/sshkey.go` | OpenList 多租户残留，139 场景基本不用 |

> ⚠️ **不存在 `op.Stat` 和 `op.Space`**（已 grep 确认）。语义对应关系：
> - README 的 `Stat` → **`op.Get` / `op.GetUnwrap`**
> - README 的 `Space` → **`op.GetStorageDetails`**（内部走 `driver.WithDetails`）
> 未来写 SDK 时要按这两个真实名字对接。

### 5.2 路由机制（`path.go:16` → `storage.go:448`）

```
rawPath "/yun/文档/a.txt"
  → utils.FixAndCleanPath          // \ → /，补前导 /，path.Clean
  → GetBalancedStorage             // storage.go:448
      → getStoragesByPath          // storage.go:314：遍历 storagesMap，IsSubPath 前缀匹配
      → 取 slashCount 最大（最长前缀），同长按 MountPath 字典序稳定排序
      → 多个 .balance 实例 → balanceMap 轮询
  → mountPath  = utils.GetActualMountPath(...)   // 剥掉 .balance 后缀
  → actualPath = FixAndCleanPath(TrimPrefix(rawPath, mountPath))
```

后续所有 op 调用都用 `actualPath`。**GetBalancedStorage 在多个 `.balance` storage 时是轮询的**，同一 rawPath 多次调用可能拿到不同 driver 实例。

### 5.3 注册流程（编译期，`driver.go:18`）

```
drivers/yun139/meta.go:31  init() → op.RegisterDriver(func() driver.Driver { ... })
  ├─ tempDriver := driver()              // 实例化一个临时实例（有构造副作用）
  ├─ registerDriverItems(tempDriver.Config(), tempDriver.GetAddition())
  │   ├─ getMainItems(config)           // driver.go:61 按 Config 位开关拼通用表单项
  │   └─ getAdditionalItems(addition)   // driver.go:178 反射 Addition 结构体
  │        读 json/ignore/default/options/required/help/type tag → driver.Item
  │        root_folder_id / root_folder_path 无 default 则取 Config.DefaultRoot 并置 Required
  ├─ driverInfoMap[config.Name] = driver.Info{Common, Additional, Config}
  └─ driverMap[config.Name] = driver
```

⚠️ 完全依赖 `init()`：**使用层必须 blank-import 驱动包**（`_ "github.com/milin2436/cloudfs-139/drivers/yun139"`），否则 `GetDriver` 报 `no driver named: X`。
⚠️ `driverMap` / `driverInfoMap` 是**无锁 map**，只能 init 期写。

### 5.4 每个核心操作的实现骨架（写新操作时照抄）

| 操作 | 位置 | op 层做了什么 | driver 方法 |
|---|---|---|---|
| `List` | `fs.go:26` | 状态检查 → 规范化 → dirCache 命中 → `listG.Do` → driver.List → wrapObjsName → SortFiles → ExtractFolder → 异步 Hook → 写缓存（ttl 受 `CustomCachePolicies` 覆盖；空目录则 `deleteDirectoryTree`） | `List` |
| `Get` | `fs.go:128` | 根路径走 `GetRooter` / `IRootId` / `IRootPath`（三者皆无直接报错）→ dirCache → `driver.Getter` → 回退 list+validator | `Getter` |
| `Link` | `fs.go:227` | 算 LinkCacheMode（含 IP/UA 维度）→ `linkCache.GetType` → **外层 `for {}` 循环**重试 `AcquireReference` → `linkG.Do` → driver.Link | `Link` |
| `Put` | `fs.go:608` | `defer file.Close()`（**在状态检查之前**）→ OnlyIndices 处理 → 存在同名：空文件先删 / `NoOverwriteUpload` 走两阶段 rename / 否则 `file.SetExist(fi)` 走秒传 → MakeDir → NoWrite 检查 → 流式无大小则 `CacheFullAndWriter` → driver.Put → 清 linkCache + UpdateObject + Hook → 两阶段收尾 | `Put` / `PutResult` |
| `MakeDir` | `fs.go:305` | `mkdirG.Do` → 已存在目录返回 nil、已存在**文件**返回 `"file exists"` → **递归建父目录** → NoWrite → driver | `Mkdir` / `MkdirResult` |
| `Move` | `fs.go:372` | 两端规范化 → 禁移动根 → `srcDir==dstDir` 返回 `"move in place"` → NoMove → NoWrite(dst) → driver → 删两端 linkCache + UpdateObject | `Move` / `MoveResult` |
| `Rename` | `fs.go:448` | 禁改根 → NoRename → driver → 删旧/新名 linkCache → UpdateObject。⚠️ **`dstName` 是纯名字不是路径** | `Rename` / `RenameResult` |
| `Copy` | `fs.go:510` | `dstDir==Dir(src)` 返回 `"copy in place"` → driver → 只更新目标目录缓存 | `Copy` / `CopyResult` |
| `Remove` | `fs.go:574` | 禁删根 → **`errs.IsObjectNotFound` 直接返回 nil（幂等）** → NoRemove → driver → `removeDirectoryObject` | `Remove` |
| `Other` | `fs.go:284` | `FsOtherArgs{Path,Method,Data}` → `driver.Other{Obj,Method,Data}` → 返回 `any` | `Other` |
| 空间 | `storage.go:468` | 状态检查 → `driver.WithDetails` → detailCache（键用 `GetActualMountPath`）→ `detailsG.Do` → driver.GetDetails | `GetDetails` |

### 5.5 singleflight 组（互斥边界）

`op/fs.go:23 listG`、`op/fs.go:224 linkG`、`op/fs.go:303 mkdirG`、`op/storage.go:466 detailsG`、`op/archive.go:29/205/399`、`op/user.go:12`、`op/setting.go:15/20`、`op/sharing.go:43`、`op/meta.go:20`。

⚠️ **Move / Copy / Rename 没有 singleflight**，并发写同一路径存在竞态。
⚠️ 全部 `Do` 调用忽略第三个返回值 `shared`（`_, err, _ :=`），保持这个写法。

---

## 6. 驱动核心：`drivers/yun139`（3850 行）

| 文件 | 行 | 内容 |
|---|---|---|
| `meta.go` | 37 | `Addition` 结构体 + `driver.Config` + `init()` 注册 |
| `types.go` | 465 | 所有 API 请求/响应结构体 + 5 个账号类型常量（:7-13）+ `DiskQuotaDetail` |
| `driver.go` | 1174 | `Yun139` 结构体 + 全部 Driver 方法实现 + `GetDiskQuota` |
| `util.go` | 1771 | HTTP 通道 + 签名 + 加密 + 登录 + Token 刷新 + `getDiskQuotaDetail` |
| `share.go` | 403 | 外链（outlink）分享：创建/列表/撤销 + `jsonInt64`。见 §6.9 |

### 6.1 `Addition` 配置（`meta.go:8-23`）

| 字段 | 必填 | 说明 |
|---|---|---|
| `Authorization` | 是 | Basic 凭证，base64(`类型:手机号:token`)。空且非 share → 触发密码登录（`driver.go:48-59`） |
| `Username` / `Password` | 是 | 手机号 + 明文密码；提交前 `sha1("fetion.com.cn:"+pwd)`（`util.go:1293`） |
| `MailCookies` | 是 | **必须含 `RMKEY=`**（step2 依赖，`util.go:1250-1257`）；登录后被 cookie jar 覆盖回写 |
| `RootFolderID`（嵌入 `driver.RootID`） | 否 | 默认按类型注入：personal_new=`"/"`，personal=`"root"`，group=`CloudID`，share=`"root"`，family=`getFamilyRootPath()` 结果并**持久化**（`driver.go:110-145`） |
| `Type` | 否 | `personal_new,family,group,personal,share`，默认 `personal_new`。**全部业务分支的开关** |
| `LinkID` | share 必填 | 多分享用 `,` `;` 换行分隔；`link_id#password` 表示带密码 |
| `CloudID` | family/group 必填 | 上传时缺失直接报错（`driver.go:769-771`） |
| `UserDomainID` | 否 | Cookie `ud_id`。**配额查询不依赖它**（`GetDetails` 的空 body 就够用，见 §6.10）；但一旦填了就必须是真的，否则 `01000001 参数错误`。另一个用途是 family/group Copy 的 `commonAccountInfo.accountUserId`。密码登录时 step3 自动写入 |
| `CustomUploadPartSize` | 否 | 0 = 自动（>30GB→512MB，否则 100MB，`driver.go:682`） |
| `ReportRealSize` | 否 | 默认 true；false 时老上传链路报 `totalSize: 0` |
| `UseLargeThumbnail` | 否 | 默认 false；true 优先取 `Style` 含 `Large` 的缩略图（`util.go:954`） |
| `UseOldStreamUpload` | 否 | 默认 false；仅影响 group/family，不支持秒传 |

`driver.Config`：`{Name:"139Yun", LocalSort:true, ProxyRangeOption:true}`（`meta.go:25`）。注册时额外 `d.ProxyRange = true`（`meta.go:31-36`）。

### 6.2 yun139 实际实现的接口（`grep "^func (d \*Yun139)"` 已核对）

| 方法 | 行 | 对应接口 |
|---|---|---|
| `Config` | 38 | Meta |
| `GetAddition` | 42 | Meta |
| `Init` | 46 | Meta |
| `InitReference` | 155 | `Reference` |
| `Get` | 164 | `Getter`（**仅 share**） |
| `Drop` | 177 | Meta |
| `List` | 185 | Reader |
| `Link` | 208 | Reader |
| `MakeDir` | 242 | `Mkdir` |
| `Move` | 302 | **`MoveResult`**（返回 `srcObj`，未重新读取） |
| `Rename` | 416 | `Rename` |
| `Copy` | 528 | `Copy` |
| `Remove` | 600 | `Remove` |
| `getPartSize` | 682 | 私有 |
| `Put` | 693 | `Put` |
| `Other` | 1066 | `Other`（**仅 `video_preview`**） |
| `GetDetails` | 1091 | `WithDetails` |
| `GetDiskQuota` | 1116 | **无任何框架接口对应** —— 原始配额报表（含 `quotaList`），见 §6.10 |
| `GetUserInfo` | 1125 | **无任何框架接口对应** —— 只能被外部用类型断言直接调用 |
| `fetchProfile` | 1142 | 私有 |

❌ 未实现：`PutURL`、`GetRooter`、`Archive*`（三个）、`LinkCacheModeResolver`、`DirectUploader`、`MkdirResult`/`RenameResult`/`CopyResult`/`PutResult`（都是无 Result 的简化版）。

### 6.3 账号类型分支矩阵

账号类型常量：`types.go:7-13` → `MetaPersonal` / `MetaFamily` / `MetaGroup` / `MetaPersonalNew` / `MetaShare`。

⚠️ **只有 `isFamily()` / `isGroup()` / `isShare()` 三个谓词**（`util.go:44/48/52`），**没有 `isPersonal()` / `isPersonalNew()`**。personal 两态一律靠 `switch d.Addition.Type` 或 `d.Addition.Type == MetaPersonalNew` 直接比较。

| 类型 | 主机来源 | 签名头 | 报文加密 | 路由策略 |
|---|---|---|---|---|
| `personal_new` | `d.PersonalCloudHost`（route 返回 `modName=personal` 的 httpsUrl） | 新式：`X-Yun-*` + `Mcloud-Sign`（驼峰）+ `Mcloud-Route: 001` | 明文 JSON | `x-SvcType:1` |
| `personal` | **硬编码** `https://yun.139.com`（`util.go:266`） | 老式：`mcloud-sign`（全小写） | 明文 JSON | `x-SvcType:1` |
| `family` | `https://yun.139.com`（`post`）+ `d.FamilyCloudHost`（`andAlbumRequest`） | 老式 | 明文；相册 openApi 走 AES-CBC | **`x-SvcType:2`** |
| `group` | `https://yun.139.com`（`post`）+ `d.GroupCloudHost`（`newPost`/新上传） | 老式（新上传走 X-Yun 头） | 明文；`copyContentCatalog` 走 AES-CBC | `x-SvcType:1` |
| `share` | `https://share-kd-njs.yun.139.com/yun-share`（`util.go:525`） | **无 mcloud-sign**，改 `X-Yun-Api-Version: v1` + `hcy-cool-flag`（`util.go:501`） | **全部 AES-128-CBC** | 不适用 |

### 6.4 操作 × 账号类型 → 端点映射

| 操作 | personal_new | personal | family | group | share |
|---|---|---|---|---|---|
| `List` | `POST /file/list`（游标分页） | `/orchestration/personalCloud/catalog/v1.0/getDisk` | `.../familyCloud-rebuild/content/v1.2/queryContentList` | `.../group-rebuild/content/v1.0/queryGroupContentList` | `getOutLinkInfoV6`（多分享合并） |
| `Link` | `/file/getDownloadUrl`（优先 `cdnUrl`+`cdnSwitch`） | `.../downloadRequest` | `.../getFileDownLoadURL` | `.../getGroupFileDownLoadURL` | `getContentInfoFromOutLink` + `dlFromOutLinkV3` |
| `MakeDir` | `/file/create` | `.../createCatalogExt` | `.../cloudCatalog/v1.0/createCloudDoc` | `.../catalog/v1.0/createGroupCatalog` | `NotImplement` |
| `Move` | `/file/batchMove` | `createBatchOprTask`（task 3 / action 304） | `/isbo/openApi/createBatchOprTask`（主机 `group.yun.139.com/hcy/mutual/adapter`，校验 `Result.ResultCode`） | `.../task/v1.0/createBatchOprTask`（task 3） | `NotImplement` |
| `Rename` | `/file/update` | 目录 `updateCatalogInfo` / 文件 `updateContentInfo` | 目录 **加密** `/modifyCloudDocV2`；文件 `photoContent/v1.0/modifyContentInfo` | 目录 `modifyGroupCatalog` / 文件 `modifyGroupContent` | `NotImplement` |
| `Copy` | `/file/batchCopy` | `createBatchOprTask`（action 309） | 加密 `/copyContentCatalog` | `handleMetaGroupCopy` → 加密 `/copyContentCatalog` | `NotImplement` |
| `Remove` | `/recyclebin/batchTrash`（**进回收站，无恢复接口**） | `createBatchOprTask`（task 2 / action 201） | `.../familyCloud-rebuild/batchOprTask/...` | `.../task/v1.0/createBatchOprTask`（task 2，**用完整路径**） | `NotImplement` |
| `Put` | 新链路 `/file/create`→`/file/getUploadUrl`→`/file/complete` | 老链路 `pcUploadFileRequest` → XML 回执 PUT | 默认新链路 `/dynamic/file/*`（`UseOldStreamUpload` 时走老 `getFileUploadURL`） | 同 family（`groupType=2`） | `NotImplement` |
| `Other` | `/videoPreview/getPreviewInfo`（`video_preview`） | — | `/hcy/user/getUserInfo`（profile） | — | — |

### 6.5 HTTP 请求通道（`util.go`）

| 函数 | 行 | URL 拼接 |
|---|---|---|
| `request` | 137 | 调用方给全 URL |
| `requestRoute` | 210 | 固定 `https://user-njs.yun.139.com/user/route/qryRoutePolicy` |
| `post` | 265 | `"https://yun.139.com" + pathname` |
| `newRequest` | 823 | 调用方给全 URL |
| `personalPost` | 884 | `getPersonalCloudHost() + pathname` → 走 `newRequest` |
| `newPost` | 890 | family/group → `getGroupCloudHost()`，其余 → `getPersonalCloudHost()` |
| `isboPost` | 904 | `"https://group.yun.139.com/hcy/mutual/adapter" + pathname` |
| `sharePost` | 524 | `"https://share-kd-njs.yun.139.com/yun-share" + pathname` |
| `andAlbumRequest` | 1613 | `getFamilyCloudHost() + "/andAlbum/openApi" + pathname` |
| `yun139EncryptedRequest` | 1430 | 全 URL 透传，加密通道 |

**新增一个 API 调用的标准写法**（`util.go:485` 范例）：

```go
func (d *Yun139) groupGetLink(contentId string, path string) (string, error) {
    data := d.newJson(base.Json{"contentID": contentId, "groupID": d.CloudID, "path": path})
    res, err := d.post("/orchestration/group-rebuild/groupManage/v1.0/getGroupFileDownLoadURL", data, nil)
    if err != nil {
        return "", err
    }
    return jsoniter.Get(res, "data", "downloadURL").ToString(), nil
}
```

模式：`d.newJson(base.Json{...})` 组 body → 选通道函数 → 响应结构体定义在 `types.go` → 取值用 `jsoniter.Get(res, ...)`。**请求层本身不设超时不重试**，全靠 `base.RestyClient` 的全局默认（3 次重试 / 30s 超时）。

### 6.6 签名与加密

**签名 `calSign`（`util.go:67`）**：`encodeURIComponent(body)` → 按字符切分 + 字典序排序 → base64 → `MD5(body) + MD5(ts+":"+randStr)` → 再 `MD5` 取大写。`ts` 格式 `2006-01-02 15:04:05`。
⚠️ 注意老式头名全小写 `mcloud-sign`，新式是驼峰 `Mcloud-Sign`。

**加密（`util.go:1300-1374`）**：

| 函数 | 位置 | 说明 |
|---|---|---|
| `aesCbcEncrypt` / `aesCbcDecrypt` | 1341 / 1356 | IV 随机 16 字节并**前置到密文**，base64 传输 |
| `aes_ecb_decrypt` | 1320 | 手写逐块 ECB，**仅** step3 第二层用 |
| `pkcs7_pad` / `pkcs7_unpad` | 1300 / 1307 | — |
| `sortedJsonStringify` | 1374 | 键排序后序列化（加密前置步骤） |
| `yun139EncryptedRequest` | 1430 | 请求体 AES-CBC → base64；响应首字节是 `{` 则视为明文降级处理 |

**硬编码密钥**（139 改版时最先失效）：

| 常量 | 位置 | 强度 | 用途 |
|---|---|---|---|
| `KEY_HEX_1` | `util.go:39` | 24 字节 = **AES-192** | `/user/thirdlogin` 第一层、`andAlbumRequest` 相册/家庭云 openApi |
| `KEY_HEX_2` | `util.go:40` | 16 字节 = **AES-128** | `/user/thirdlogin` 第二层（hex → ECB） |
| `shareAesKeyHex` | `util.go:499` | 16 字节 = **AES-128**，即 `hex("PVGDwmcvfs1uV3d1")` | 所有 share 端点 |

其它硬编码：`clientkey_decrypt: "l3TryM&Q+X7@dzwk)qP"`（:1514）、`clienttype: 886`、`cpid: 507`、`version: 20250901`、`mcloud-version: 7.14.0`、`x-DeviceInfo` 指纹串、`yun.139.com` / `group.yun.139.com` 主机。

### 6.7 鉴权与 Token 生命周期

**密码登录三步**（`loginWithPassword` `util.go:1585`）：

1. `step1_password_login`（:1119）→ `POST https://mail.10086.cn/Login/Login.ashx`，form `Password=sha1Hash(...)`；`NoRedirectPolicy()` 禁跳转，从 `Location`/`Set-Cookie` 正则取 `sid`（`sid=` / `Os_SSo_Sid=`）与 `cguid`，再用 cookie jar 重写 `d.MailCookies`（:1226-1234）。
2. `step2_get_single_token`（:1239）→ `POST https://smsrebuild1.mail.10086.cn/setting/s?func=umc:getArtifact&sid=&cguid=`，Cookie 只带 `RMKEY`，响应 `var.artifact` 即 `dycpwd`。
3. `step3_third_party_login`（:1508）→ `POST https://user-njs.yun.139.com/user/thirdlogin`；请求体 AES-CBC(KEY_HEX_1) 加密；响应先 AES-CBC 解一层得 hex `data`，再 KEY_HEX_2 做 **AES-ECB** 解密 → 取 `authToken` / `account` / `userDomainId`。

**产出**：`d.UserDomainID = userDomainId`；`newAuthorization = base64("pc:<account>:<authToken>")`；`op.MustSaveDriverStorage(d)` 立即持久化。

**离线刷新 `refreshToken`（`util.go:83`）**：解 base64 → 按 `:` 切 3 段 → 第 3 段按 `|` 切取第 4 段为过期毫秒 → `expiration -= now` → **>15 天直接跳过**（:106）；`<0` 报 `authorization has expired`；否则 POST XML `<root><token>…</token><account>…</account><clienttype>656</clienttype></root>` 到 `https://aas.caiyun.feixin.10086.cn:443/tellin/authTokenRefresh.do`，`Return=="0"` 时重组 base64 并持久化。**失败自动降级为 `loginWithPassword`**（:124）。

**cron 12h**（`driver.go:101-107`）：`cron.NewCron(time.Hour*12)` + `Do(refreshToken)`；`Drop`（:177-179）负责 `Stop()`。
⚠️ **只有 `d.ref == nil` 的主实例才创建 cron 并拉取路由策略**（`driver.go:47-109`）。因此被引用实例必须先于引用实例 Init，否则 `PersonalCloudHost` 为空、请求打到空 URL。

**`UserDomainID` 的用途**（已收敛到一个）：family/group Copy 的
`commonAccountInfo.accountUserId`（`driver.go:582`、`util.go:1649`）。配额查询
**不需要它** —— `getDiskQuotaDetail`（`util.go:1104`）只在
`d.UserDomainID != ""` 时才把它放进 body，空 body 也能取到配额，见 §6.10。

**手机号解码**：`d.Account = splits[1]`（`util.go:96`），即 Authorization base64 后冒号分隔的第二段；`getAccount()`（:1012）统一提供并透明转发给 `ref`。

### 6.8 share 类型特殊语义

- 单 ref 的 ID 编码：`PathEscape(linkID)|PathEscape(password)|PathEscape(nodeID)`；多 ref：`"shares:" + base64(JSON)`（`util.go:535,537-584`）。
  ⚠️ **任何自定义代码处理 share 对象 ID 前必须先 `decodeShareRefs`**，否则会把编码串当 `coID` 发给服务端。
- 多分享同名目录会合并 refs（`util.go:668-708`）。
- share 下载 URL 三级回退：`extInfo.cdnDownloadUrl` > `redrUrl` > `downloadUrl`（:802-810）。
- share 无 `account` 时只给 `PresentURL`，否则报错（:780-785）。
- share 可无 Authorization：`Init` 跳过 refreshToken（`driver.go:48`），`shareHeaders` 无 auth 时不设 `Authorization`（:518-520）→ **只能预览不能下载**。

### 6.9 外链（outlink）分享网关 —— 与 §6.8 完全不同的一套

⚠️ **§6.8 讲的是"读别人发来的分享"，本节讲的是"自己创建分享"。两套网关、两套
加密策略、两套账号要求，代码上也没有任何共享。**

实现：`drivers/yun139/share.go`（驱动）→ `sdk/cloudfs/share.go`（SDK）→
`cmd/cloudfs/share.go`（CLI）。踩坑与错误码表见 DEV.md §3.5 / §4.15。

| | §6.8 share 账号（消费） | §6.9 outlink（创建） |
|---|---|---|
| host | personal 网关 | `https://yun.139.com/orchestration/personalCloud-rebuild/outlink/v1.0` |
| 传输 | `hcy-cool-flag: 1` + AES-256-CBC | **明文 JSON，不加任何头** |
| 账号 | `Type: "share"` | 仅 `personal_new` / `personal`（`checkShareSupport()` 本地拒绝其它类型） |

**三个端点**（body 都是 `{actionReq: {...}}` 单键包裹）：

| 方法 | 路径 | body 的 key |
|---|---|---|
| `CreateOutLink` | `/getOutLink` | `getOutLinkReq` |
| `ListOutLinks` | `/getOutLinkList` | `getOutLinkLstReq` |
| `DeleteOutLinks` | `/delOutLink` | `delOutLinkReq` |

**为什么不加 `hcy-cool-flag`**：那是 `shareHeaders()` 给 §6.8 的 AES 网关用的，
加了会强制响应变成 AES base64 blob，请求体就得用 32 字节硬编码 key 加密。
outlink 网关不带这个头就是明文 JSON —— **加这个头只会把简单问题变成密码学问题**。
签名永远算在**明文 body** 上。

**复用方式**：直接调驱动已有的 `newRequest()`（`util.go:823`，mcloud-sign 那套）
打到一个不同的 URL 即可，不用新建 resty 客户端。
注意 `newRequest` 在 `success: false` 时会丢弃 body 只返回 `errors.New(e.Message)`，
所以分享功能的错误细节只能靠 CLI/SDK 的返回信息判断，看不到原始 JSON。

**必须记住的四条**（一条都会导致只有真机才报错的问题，详见 DEV.md §4.15）：

1. `commonAccountInfo` 要放在 **action 对象内部**，放顶层 → `1010010015 鉴权失效`。
   这是唯一用**手机号**（不是 Cookie Authorization）标识账号的地方。
2. body key 与路径拼写不一致：`getOutLinkLstReq`（`Lst`）↔ `/getOutLinkList`（`List`）。
3. `qryType` 没有默认值：`1` = 我发出的，`2` = 发给我的，`0` → `1010000500`。
   列表还必须带 `srt: 0, srtDr: 0`。
4. 139 的数字字段类型不稳定（`count` 是字符串），用 `jsonInt64` 同时吃两种。

**限制**：单次分享 ≤ 50 个对象，撤销也 ≤ 50；`expires` 只能是 0 / 1 / 7 天；
提取码由服务端生成（没有"自定义码"参数），CLI 的 `--password` 是布尔开关。
`openCount` 字段**不是**有效分享数（一条活着的分享返回 0），别拿它当 active 用。

**使用层接入方式**：走"食谱 8 路径 2"（类型断言），本地接口
`outlinkShareable` 定义在 `sdk/cloudfs/share.go:80`，和 `userinfo.go` 的
`userInfoProvider` 完全同一个模式 —— 框架没有分享接口，所以只能断言，
不能做成通用能力。配额分项是同一个模式的第三个使用者，见 §6.10。

### 6.10 配额（`quota/detail`）：`space` 的显示与契约

`POST https://user-njs.yun.139.com/user/disk/quota/detail`，走 §6.5 的
mcloud-sign 通道（`util.go` 的 `request()`）。**请求体可以为空**，鉴权只看
Authorization cookie。

| body | 结果 |
|---|---|
| `{}` | ✅ `0000 请求成功` |
| `{"userDomainId":""}` | ✅ `0000` |
| `{"account":"<手机号>"}` | ✅ `0000` |
| `{"commonAccountInfo":{"account":"..."}}` | ✅ `0000` |
| `{"userDomainId":"bogus"}` | ❌ `01000001 参数错误` |
| `{"userDomainId":"bogus","commonAccountInfo":{...}}` | ❌ `01000001` |

**单位是 MB（1024 KiB）**，用恒等式钉死：
`diskSize - freeDiskSize == sum(usedSize)`，即
`20480 - (-202493) == 196894 + 26079 == 222973`。`GetDetails` 乘的
`utils.MB` 与此一致。

**139 没有目录级配额。** 前端 60 个 bundle 里配额状态只有三处，全部不按路径：
`userQuotaDetail{diskSize,freeDiskSize,quotaList[]}`、
`userDiskInfo{diskSize,freeDiskSize,isInfiniteStorage}`、
`familyCloudDiskInfo{usedSize,totalSize}`；页面唯一那条进度条就是
`(diskSize-freeDiskSize)/diskSize*100`。所以"某个路径还剩多少容量"不存在，
`space` 也不收路径参数。

**分项 ≠ 分区。** 家庭云和个人云共用一个池子，`quotaList` 分项之和可以超过
`diskSize`。本账号 20 GiB 配额、实际已用 222,973 MB ≈ 217.7 GiB，
`freeDiskSize` 为负，超额约 198 GiB —— 这就是 §0 里那个
`00010012 资源配额不足` 的原因。

实现三层：

| 层 | 位置 | 说明 |
|---|---|---|
| 驱动 | `util.go:1104` `getDiskQuotaDetail` | 只在 `UserDomainID != ""` 时才把 `userDomainId` 放进 body |
| 驱动 | `driver.go:1091` `GetDetails` | 通用三数；**已删除** `UserDomainID == ""` 的前置 guard |
| 驱动 | `driver.go:1116` `GetDiskQuota` | 导出，返回原始 `DiskQuotaDetail`（含 `quotaList`） |
| 类型 | `types.go:391` / `types.go:401` | `DiskQuotaDetail` / `DiskQuotaItem`；`quotaList` 原先是被注释掉的 |
| SDK | `quota.go:16` / `quota.go:56` | 本地接口 `diskQuotaGettable` + `Client.Quota`，单位保持 MB |
| CLI | `cmd/cloudfs/quota.go:20` | `quotaTable`：box-drawing 表格，MB 千分位 + GiB 折算列 |
| CLI | `cmd/cloudfs/quota.go:54` | `drawTable`：按显示宽度对齐，CJK 算 2 列（`isWide`） |

`cmd/cloudfs/cmds.go` 的 `space` 优先取 `Quota()`，取不到才回落 `Space()` ——
仓库目前只注册了 `yun139` 一个驱动（`drivers/yun139/meta.go:32`），所以回落
分支实际不会触发，但留给了以后的驱动。

顺带：`POST /user/status/query` 传 `{}` 会返回 `userDomainId`
（本账号 `1256537756588849073`），所以这个值本身也能自动发现，
目前还没做自动填充。踩坑细节见 DEV.md §3.6 / §4.17。

---

## 7. 初始化顺序（新建使用层时必须严格遵循）

来自 DEV.md §3.1，已逐条与代码依赖关系核对。**顺序错了会 nil panic**：

```
① conf.Conf = &conf.Config{ Database:{Type:"sqlite3"}, TempDir:..., ... }
   为什么最先：db.AutoMigrate 读 conf.Conf.Database.Type；utils.CreateTempFile 读 TempDir；
              utils.MappingName 读 FilenameCharMap；mem.MemoryGrowCheck 读 MinFreeMemory
② gorm.Open(sqlite.Open("file:cloudfs?mode=memory&cache=shared&_pragma=busy_timeout(5000)"), ...)
   必须用 github.com/glebarez/sqlite（纯 Go，无需 CGO）
   ⚠️ mode=memory 不可省：`file::memory:xxx` 会被 SQLite 当成磁盘文件名处理，
     在 cwd 写出 `:memory:xxx` 文件（内含 Authorization token），见 DEV.md §4.12
③ db.Init(d)                        // cloudfso/db/db.go:13 —— 一次性 AutoMigrate 全 8 张表，失败 log.Fatalf
④ base.InitClient()                 // cloudfso/base/client.go:24 —— 不调用则 RestyClient/HttpClient 为 nil
⑤ _ "github.com/milin2436/cloudfs-139/drivers/yun139"   // blank import，触发 op.RegisterDriver
⑥ op.CreateStorage(ctx, model.Storage{MountPath, Driver:"139Yun", Addition: jsonStr, Status:op.WORK})
   内部：SetStorage → Unmarshal Addition → Init(ctx) → storagesMap.Store → SetStatus → MustSaveDriverStorage
⑦ （可选）conf.SendStoragesLoadedSignal()
```

✅ ①②③④⑤⑥ 现在由 `sdk/cloudfs/client.go` 的 `Client.onceInit` 按此顺序调用
（⑦ `SendStoragesLoadedSignal` 未调用——CLI/SDK 不需要触发 Web 端刷新，
但如果你要接 Web 管理界面就得补上）。

---

## 8. 已知缺陷与待清理（动手前必读）

### 🔴 阻塞级

| # | 问题 | 位置 | 处理建议 |
|---|---|---|---|
| 0 | **`.gitignore` 的裸 `cloudfs` 模式把 `sdk/cloudfs/` 和 `cmd/cloudfs/` 整个排除在版本控制之外**，`git status` 完全看不到 | `.gitignore:2` | ✅ **已修**：改为 `/cloudfs` 锚定到仓库根。这**很可能就是原「可构建代码」从未进过 git 的原因**，与磁盘损坏叠加才导致不可恢复。见 DEV.md §4.11 |
| ~~1~~ | ~~`cloudfso/cache` 编译失败~~ | `weak_cache.go` | ✅ **已修**：`weak` 是 Go 1.27 才有的标准库包，本机只有 go1.26.5。加 `//go:build go1.27` 隔离而非删文件，1.27+ 工具链下自动复活。见 DEV.md §4.7 |
| ~~2~~ | ~~使用层完全缺失~~ | 仓库根 | ✅ **已修**：`sdk/cloudfs/` + `cmd/cloudfs/` 已按 README/DEV.md 契约重建，op 层全部文件操作均有入口（归档相关除外，见 #4） |
| ~~3~~ | ~~无法编译验证~~ | go 环境 | ✅ **已修**：`export GOPROXY=https://goproxy.cn,direct` 后 `go build ./...`、`go vet ./...`、`go test -race ./sdk/...` 全部通过 |
| 3b | **真实 API 联调未验证**：token 配置随磁盘丢失，使用层只在「驱动参数校验」这一层验证过，从未发起过真实 139 请求 | — | 准备一个 personal_new 的 `Authorization`，按 DEV.md §5.2 跑 `whoami` → `ls` → `get` → `put` |

### 🟡 功能失效

| # | 问题 | 位置 | 影响 |
|---|---|---|---|
| 4 | **压缩包功能整体失效**：`archive/tool.RegisterTool` 全仓库 0 调用方 → `Tools` 恒为空 map → `op/archive.go:79` 的 `GetArchiveTool` 永远返回 `errs.UnknownArchiveFormat` | `cloudfso/archive/tool/utils.go:12` | `ListArchive` / `ArchiveDecompress` / 分卷合并全部不可用。要恢复需在 `RegisterTool` 处接入 7zip-rs 之类 |
| 5 | **`archive/tool/helper.go` 5 个导出符号零调用**（`SubFile`/`CanEncryptSubFile`/`ArchiveReader`/`WrapFileInfo`/`DecompressFromFolderTraversal`/`GenerateMetaTreeFromFolderTraversal`/`MakeModelObj`） | `archive/tool/helper.go` | 死代码 |
| 6 | **多账号并发登录会污染全局 HTTP 客户端**：`step1_password_login` 用 `base.RestyClient.SetRedirectPolicy(NoRedirectPolicy())` 改的是**全局**客户端，而恢复语句 `SetRedirectPolicy(FlexibleRedirectPolicy(10))` 只在 `err == nil` 分支（:1184）。登录返回非重定向错误时会直接 `return` 而不恢复 | `drivers/yun139/util.go:1167` / `:1184` | 后续所有请求都不跟随重定向。修法：用 `defer` 恢复，或改用 `resty.NewClient()` 局部实例 |
| 7 | **`getPersonalTime` 解析失败直接 `panic(err)`** | `drivers/yun139/util.go:914` | 一次字段格式异常（新接口时间格式 `2006-01-02T15:04:05.999-07:00`）会崩掉整个进程。应返回 error |
| 8 | **`Token` 刷新无并发保护**：12h cron 里改 `d.Authorization` 并 `MustSaveDriverStorage`，与业务请求读取之间无互斥；`d.cron` / `d.ref` 读写同样裸奔 | `util.go:83` / `driver.go:101` | 需要给 `Yun139` 加 `sync.RWMutex` |
| 9 | **新上传链路同名文件静默成功**：`resp.Data.Exist == true` 时直接 `return nil`（`driver.go:789`），不报错也不覆盖。冲突处理靠「List → Rename 旧文件加 4 位随机后缀 → Remove → Rename 新文件」+ `time.Sleep(500ms)`（:861） | `driver.go:789-861` | 秒传（`RapidUpload`）实际未被利用 |

### 🟠 设计债 / 语义不一致

| # | 问题 | 位置 |
|---|---|---|
| 10 | **两套请求错误语义不一致**：`request` 在 `!e.Success` 时先 unmarshal 到 `resp`，并对 `*CreateBatchOprTaskResp` 特殊判断 `Result.ResultCode=="0"` 后 `goto SUCCESS_PROCESS` 返回 nil error（:183-207）；`newRequest` 直接 `errors.New(e.Message)`（:872）。新响应结构不会自动受益于前者 | `util.go:183` / `:872` |
| 11 | **业务失败漏报**：family 的 `Move` 检查 `Result.ResultCode`（:407），但 personal/group 的 Move、所有 Copy（`handleMetaGroupCopy` 直接忽略响应 :591）**只看 HTTP 层错误** | `driver.go:407` / `:591` |
| 12 | **双份配置不一致风险**：`conf.Config.AutoMemoryLimit/MinFreeMemory/MaxBlockLimit`（int，`config.go:127-129`）与 `conf.AutoMemoryLimit/MinFreeMemory/MaxBlockLimit`（uint64，`var.go:29-35`）是**两套独立变量**，需启动代码手动同步 | `conf/config.go` / `conf/var.go` |
| 13 | **`conf.StoragesLoaded` 读无锁**：写在锁内，读完全无保护 → race。应统一走 `conf.StoragesLoadSignal()` channel | `conf/var.go:46,57-78` |
| 14 | **`TablePrefix` 是死配置**：`conf.Conf.Database.TablePrefix`（默认 `"x_"`）从未被使用（未配置 GORM `NamingStrategy`），表名不带前缀 | `conf/config.go:17` |
| 15 | **`conf.HandleHookRateLimit` 的 setting 需 `"true"`/`"1"` 且 hooks 非空**才触发；hook 一律 `go` 异步 + `context.WithoutCancel(ctx)` | `op/fs.go:856` / `op/hook.go` |
| 16 | **`net.ServeHTTP` 0 调用方**，是为 Web 服务模式预留的基建（服务端 Range/断点续传能力已实现但未接线） | `cloudfso/net/request.go:55` |
| 17 | **`GetDb()` / `GetRangedHttpReader` 等 0 调用方**，属保留能力 | `cloudfso/db/db.go:31` |
| 18 | **品牌残留（改了就出事）**：`model/user.go:58 StaticHashSalt = "https://github.com/alist-org/alist"`、`DefaultSalt = "alist"`、`utils/random/random.go:30 Token()` 前缀 `"openlist-"`。这些参与密码哈希，**不要改** | `model/user.go` |
| 19 | **`cache` 包 GC 回调无注销机制**：构造函数把 `c.GC` append 进包级 `gcFuncs`，长生命周期进程里每次新建缓存都永久增加一个回调；`utils.go:16` 自带 `// TODO Move to bootstrap` | `cloudfso/cache/utils.go:15` |
| 20 | **`SSHPublicKey` 表名会落成 `s_s_h_public_key`**（GORM 缩写拆分），建议显式写 `TableName()` | `cloudfso/model/sshkey.go:11` |
| 21 | **`SearchNode` 按 `conf.Conf.Database.Type` 分三支**，但只有 sqlite3 分支会真的跑（其余依赖未启用的搜索功能） | `cloudfso/db/searchnode.go:59,68,71` |
| 22 | **无测试**：`*_test.go` 数量为 0 | 全仓库 |

---

## 9. 数据库层：`cloudfso/db`（GORM + 纯 Go SQLite）

全局句柄 `db.go:11 var db *gorm.DB`（未 Init 时全部调用 panic）。`db.Init`（:13）一次性 AutoMigrate 全 8 张表，失败即 `log.Fatalf`。

| 表 | 模型 | 139 场景 |
|---|---|---|
| `storage` | `model.Storage` | ✅ **唯一真用**（多账号 = 多行，`op.MustSaveDriverStorage` 回写 Addition） |
| `user` | `model.User` | ❌ 未启用（139 鉴权用 139 token） |
| `meta` | `model.Meta` | ❌ 路径级 ACL，多租户残留 |
| `setting_item` | `model.SettingItem` | ❌ OpenList 管理台残留（但 hook 机制依赖它） |
| `search_node` | `model.SearchNode` | ❌ 全文索引残留 |
| `task_item` | `model.TaskItem` | ⚠️ 可用但简陋：仅 `Key + PersistData(text)`，无主键、`Key` 不 unique，只适合单行状态 |
| `s_s_h_public_key` | `model.SSHPublicKey` | ❌ SFTP 残留 |
| `sharing` | `model.SharingDB` | ❌ 分享功能残留 |

> ⚠️ 后 5 张表的 AutoMigrate 仍会执行。内存 SQLite 下几乎无成本，文件型 SQLite 下是无谓写入。若嫌脏，可从 `db.Init` 的列表里删掉（注意 `op/setting.go` 的 hook 机制依赖 `setting_item`，删前确认是否要保留 hook）。

---

## 10. 扩展食谱（"我要加 X，改哪里"）

### 食谱 1 — 新增一个文件操作（如 `Truncate` / `GetChecksum`）

三层，缺一不可：

**(a) `cloudfso/driver/driver.go` 定义接口**（成对给出基础版 + Result 版，仿 `Mkdir`/`MkdirResult`）：

```go
type Truncate interface {
    Truncate(ctx context.Context, obj model.Obj, size int64) error
}
type TruncateResult interface {
    Truncate(ctx context.Context, obj model.Obj, size int64) (model.Obj, error)
}
```
新入参 → 加到 `cloudfso/model/args.go`（仿 `LinkArgs:21`）。

**(b) 驱动实现**：在 `drivers/yun139/driver.go` 加同名方法。op 层的类型断言自动生效，**无需注册**。

**(c) `cloudfso/op/fs.go` 加 facade**（照抄"状态检查 → 规范化 → Get → mask 检查 → 双分支断言 → 缓存维护 → hook"骨架）：

```go
func Truncate(ctx context.Context, storage driver.Driver, path string, size int64) error {
    if storage.Config().CheckStatus && storage.GetStorage().Status != WORK {
        return errors.WithMessagef(errs.StorageNotInit, "storage status: %s", storage.GetStorage().Status)
    }
    path = utils.FixAndCleanPath(path)
    rawObj, err := Get(ctx, storage, path, true)
    if err != nil {
        return errors.WithMessage(err, "failed to get object")
    }
    if model.ObjHasMask(rawObj, model.NoWrite) {
        return errors.WithStack(errs.PermissionDenied)
    }
    var newObj model.Obj
    switch s := storage.(type) {
    case driver.TruncateResult:
        newObj, err = s.Truncate(ctx, model.UnwrapObjName(rawObj), size)
    case driver.Truncate:
        err = s.Truncate(ctx, model.UnwrapObjName(rawObj), size)
    default:
        return errs.NotImplement
    }
    if err != nil {
        return errors.WithStack(err)
    }
    Cache.linkCache.DeleteKey(Key(storage, path))
    if !storage.Config().NoCache {
        if cache, exist := Cache.dirCache.Get(Key(storage, stdpath.Dir(path))); exist {
            if newObj == nil {
                newObj = &model.ObjWrapMask{Obj: rawObj, Mask: model.Temp}
            }
            cache.UpdateObject(rawObj.GetName(), wrapObjName(storage, newObj))
        }
    }
    if ctx.Value(conf.SkipHookKey) == nil && needHandleObjsUpdateHook() {
        go objsUpdateHook(context.WithoutCancel(ctx), storage, stdpath.Dir(path), false)
    }
    return nil
}
```

只读元数据（如 checksum）**不要清缓存**，只需状态检查 + `GetUnwrap` + 断言，参考 `op/fs.go:771 GetDirectUploadTools`。

### 食谱 2 — 新增一个 139 API 端点

见 §6.5 的完整范例：`util.go` 加 `xxxGet` 方法 → 响应结构体进 `types.go` → 选通道函数（`post` / `personalPost` / `newPost` / `isboPost` / `sharePost` / `andAlbumRequest`）→ 需要加密就换 `yun139EncryptedRequest`。
要对外暴露：走 `Other`（已有，`driver.go:1066`，加一个 `case method`）或按食谱 1 建正式接口。

### 食谱 3 — 新增配置字段（Addition）

`drivers/yun139/meta.go` 的 `Addition` 加字段，tag 约定：`json`（必填）、`type`（`text`/`select`/`number`/`bool`）、`required`、`secret`、`default`、`options`、`help`。
因为 `Addition` 被 `Yun139` 嵌入，驱动里直接写 `d.<Field>`。范例见 `UseLargeThumbnail`（`meta.go:21` 定义 → `util.go:954` 消费）。
注册时 `op.getAdditionalItems`（`driver.go:178`）反射自动生成表单项。

### 食谱 4 — 支持新账号类型

四处必改：

1. `types.go:7-13` 加 `const MetaXxx string = "xxx"`
2. `meta.go:15` 的 `options:` 追加 `"xxx"`
3. `util.go:44-54` 仿 `isGroup()` 加 `isXxx()` 谓词
4. `driver.go` 的 `Init` / `List` / `Link` / `MakeDir` / `Move` / `Rename` / `Copy` / `Remove` / `Put` 每个 `switch d.Addition.Type` 加 `case`
   - 需要新主机：`util.go:890` 的 `newPost` switch 加分支 + `driver.go:79-88` 的 route 策略循环加 `case "xxx"`

### 食谱 5 — 加新接口能力（回收站 / 搜索 / 截断）

先在 `cloudfso/driver/driver.go` 定义接口（框架通过类型断言发现），再按食谱 1 的 (b)(c) 两步。**框架目前完全没有「回收站」「搜索」「截断」接口**，都要从 `driver.go` 开始加。
现状参考：回收站只在 personal_new 的 `Remove` 里走了 `/recyclebin/batchTrash`，**没有列回收站/恢复**。

### 食谱 6 — 持久化一个新实体（如下载任务表）

1. `cloudfso/model/downloadtask.go`：结构体 + gorm tag，**显式写 `func (DownloadTask) TableName() string`** 规避缩写拆分
2. `cloudfso/db/downloadtask.go`：按 `db/meta.go` 写法写 CRUD。列名是 SQL 保留字（`order`/`group`/`parent`）必须用包私有 `columnName("order")` 包裹 → **所以只能写在 `cloudfso/db` 内**
3. 迁移：`db.AutoMigrate(new(model.DownloadTask))` 支持增量追加（`db.go:21`），**不必改 `Init`**
4. 上层业务放 `cloudfso/op/`，照抄 `op/user.go` / `op/setting.go` 的 singleflight + 缓存组合

> 若只是"存一段 JSON 快照"，可直接复用 `task_item` 表 + `db.GetTaskDataFunc/UpdateTaskDataFunc`（`tasks.go:25/39`）。但注意读闭包内部会 `<-conf.StoragesLoadSignal()` 阻塞到存储加载完成。

### 食谱 7 — 新增 op 层子能力（任务队列 / 周期任务）

- 新文件 `cloudfso/op/task.go`，全局状态用 atomic（照抄 `recursive_list.go:19-20`）：
  ```go
  var TaskCancel = atomic.Pointer[context.CancelFunc]{}
  var TaskCount  = atomic.Uint64{}
  func BeginTask() error {
      ctx, cancel := context.WithCancel(context.Background())
      if !TaskCancel.CompareAndSwap(nil, &cancel) { cancel(); return errors.New("already running") }
      ...
  }
  ```
- 限速用 `golang.org/x/time/rate`（`recursive_list.go:57`、`fs.go:826` 已有用法）
- 周期能力直接用 `cloudfso/cron`（`cron.go:10 NewCron` / `:17 Do` / `:32 Stop`）—— 注意 `Do` 是 fire-and-forget，`f` 里 panic 不会 recover，会打死进程
- 要缓存就扩 `CacheManager`（`cache.go:14` 加字段 → `:22 NewCacheManager` 初始化 TTL → `:148 ClearAll` 补一行）
- "写入后触发"用 `RegisterObjsUpdateHook`（`hook.go:25`），不要自建监听

### 食谱 8 — 让使用层拿到驱动自定义方法（三条路径，侵入性递增）

1. **通用通道（零改动）**：`op.Other(ctx, storage, model.FsOtherArgs{Path, Method, Data})`（`fs.go:284`）→ `driver.Other`。yun139 已实现（`driver.go:1066`）。
2. **类型安全通道**：`storage, actualPath, _ := op.GetStorageAndActualPath(path)` → `if x, ok := storage.(SomeIface); ok {...}`。与 op 层内部写法完全一致（`fs.go:288`、`:772`、`:783`）。
   **仓库里已有三个使用者**：`sdk/cloudfs/userinfo.go`（账户信息，本地接口
   `userInfoProvider`）、`sdk/cloudfs/share.go`（分享，本地接口
   `outlinkShareable`，见 §6.9）、`sdk/cloudfs/quota.go`（配额分项，本地接口
   `diskQuotaGettable`，见 §6.10）。**加一个不属于 op 层的能力（分享、搜索、
   回收站）就用这条路** —— 别为了一个功能去改 `cloudfso/op` 的公共接口。
3. **元信息暴露（不需要实例）**：`op.GetDriverInfoMap()` / `GetDriverNames()` / `GetDriver(name)`。

### 食谱 9 — 给下载加 Range / 断点续传

- **服务端**：`net/request.go:55 ServeHTTP` 已支持多 Range + multipart，配 `net/util.go:243 checkPreconditions`（RFC7232 的 If-Match/If-None-Match/If-Range）、`http_range.ParseRange`。⚠️ `size < 0` 直接 500，**不支持未知大小**
- **客户端**：`net/serve.go:75 Download`（并发分片 + 重试续传 + 429/502/503/504 抖动退避 + 并发收缩）、`stream/util.go:29 GetRangeReaderFromLink`（三档：本地 RangeReader / URL+并发 / 单请求）、`stream/stream.go:275 SeekableStream.RangeRead`、`stream/stream.go:405 NewReadAtSeeker`
- **限速**：`stream/limit.go:31-34` 四个全局 `Limiter`，初始化处赋 `rate.NewLimiter(...)` 即可（默认 nil）
- 139 因**不返回 206**，特判在 `stream/util.go:137 checkContentRange`

### 食谱 10 — HTTP 超时 / UA / 代理 / 抓包调试

- 超时/UA：`base/client.go:19,21-22`（`DefaultTimeout=30s`、`UserAgent`）→ 改成从 `conf` 读
- 代理：`net/util.go:327 SetProxyIfConfigured` / `:337 SetRestyProxyIfConfigured`（读 `conf.Conf.ProxyAddress`，已有开关）
- 抓包：`base.EnableHTTPCapture()` → 做事 → `base.FormatCaptures(base.GetHTTPCaptures())`
  ⚠️ 抓包只覆盖 `RestyClient` / `NoRedirectClient`，**不覆盖** `base.HttpClient`（大文件 PUT 走它，`drivers/yun139/util.go:1079`）——要抓需在 `net/request.go:296 safeTransport` 外再套一层 tee transport
  ⚠️ 关闭抓包时 `captureTransport` 走 `http.DefaultTransport`，会**丢弃**代理/TLS/Jar，抓包与不抓包的出站行为不一致
  ⚠️ `captures` 无容量上限，长时抓大响应会吃内存

### 食谱 11 — 可配置限速

`stream/limit.go:31-34` 的 `ClientDownloadLimit` / `ClientUploadLimit` / `ServerDownloadLimit` / `ServerUploadLimit` 四个全局 `Limiter`，目前默认 nil、**无外部赋值点**。
- 上传限速生效点：`driver/utils.go:39-55` 的 `NewLimitedUploadStream/File` + `ServerUploadLimitWaitN`
- 下载限速生效点：`stream/util.go:58-70,100-106`、`stream/limit.go:132 RateLimitRangeReaderFunc`
- 全局并发闸门：给 `net.DefaultConcurrencyLimit`（`serve.go:39`）赋值，超限下游收到 429 语义错误

---

## 11. 陷阱清单（跨包）

**全局可变状态（多线程 CLI/SDK 场景下全部是共享的）**

| 变量 | 位置 | 备注 |
|---|---|---|
| `base.RestyClient` / `NoRedirectClient` / `HttpClient` | `base/client.go:14-16` | 全局单例；`util.go:1167` 会临时改 RedirectPolicy（见缺陷 #6） |
| `base.UploadStateCache` | `base/upload.go:13` | 32 分片，TTL 20min；`GetUploadProgress` 用 `GetDel` 保证单一消费者 |
| `base.captMu` / `captures` / `reqSnaps` | `base/http_capture.go:27-33` | `reqSnaps` 以 `"METHOD URL"` 为键，**并发同 URL 会互相覆盖快照** |
| `net.HttpClient()` | `net/request.go:269` | `sync.Once` 单例 + 最多 10 跳重定向；与 `base.HttpClient` 是**两个不同实例** |
| `net.DefaultConcurrencyLimit` | `net/serve.go:39` | nil = 不限 |
| `stream.Client/ServerDownloadLimit`、`Client/ServerUploadLimit` | `stream/limit.go:31-34` | 默认 nil。⚠️ `RateLimitReader.WaitN` 前有 nil 判空，但 `driver.ServerUploadLimitWaitN`（`driver/utils.go:56`）**直接解引用**，调用前必须初始化 |
| `conf.Conf` / `conf.Conf` 相关 | `conf/var.go:18` | 包内无 `init()` 兜底，**必须自行判空** |
| `conf.StoragesLoaded` / `RawIndexHtml` / `ConfigPath` / `URL` | `conf/var.go` | 全部可能为 nil/空串 |
| `db.db` | `cloudfso/db/db.go:11` | Init 前任何调用都 panic |
| `model.LoginCache`（`*cache.MemCache[int]`） | `model/user.go:33` | 全局登录失败计数，跨用户共享；`DefaultLockDuration`/`DefaultMaxAuthRetries` 是可变 package var，测试易污染 |
| `op.driverMap` / `op.driverInfoMap` | `op/driver.go:15-16` | 无锁 map，**只能 init 期写** |
| `op.objsUpdateHooks` / `storageHooks` / `settingItemHooks` / `settingChangingCallbacks` | `op/hook.go` / `op/setting.go:25` | `Register*` 直接 append，**必须在启动阶段注册完** |
| `op.adminUser` / `op.guestUser` | `op/user.go:13-14` | 懒加载且无锁，`UpdateUser` 会置 nil |
| `op.MigrationSettingItems` | `op/setting.go:205` | 外部赋值，nil map 只导致迁移不生效 |
| `cache.gcFuncs` | `cache/utils.go:10` | **无注销机制**，见缺陷 #19 |

**上下文取消语义**

- `net.downloader` 用 `context.WithCancelCause`（`serve.go:191`）；`interrupt()`（:294，幂等，用 `maxPos` 判定）把 `context.Canceled` 转 nil、其它原因转 error
- `net.ServeHTTP` **必须**传 `r.Context()`（:113），否则请求断开后 Copy 仍阻塞；`context.Cause==Canceled` 时静默返回（:196）
- `stream.RateLimit*` / `ReaderWithCtx` / `PipeBuffer` 都在 I/O 前检查 `ctx.Err()`
- **想中断大文件传输，应该 `Close` 返回的 `ReadCloser`**，而不是取消外层 ctx 后继续读
- op 的 hook 一律 `context.WithoutCancel(ctx)` 脱离调用方生命周期

**Close 调用约定（漏掉就泄漏远端连接/临时文件）**

- `net.ServeHTTP`：`defer RangeReadCloser.Close()`（`request.go:56`）
- `stream` 家族：统一 `utils.Closers.Add` 累积（`stream.go:209,264,281`）
- `HybridCache`：`runtime.AddCleanup` + 显式 `Close`（`hybrid_cache.go:103,110`）
- `buffer.PipeBuffer.Close`：置 nil block，**不可重复使用**
- 后备存储 Close 会 `os.Remove` 临时文件（`hybrid_cache/file.go:33,58`）
- op 的 `Put`：`defer file.Close()` 在状态检查**之前**，避免任何返回路径漏关

**并发安全边界**

- `hybrid_cache.HybridCache` 与 `buffer.PipeBuffer`：各自内部加锁但**整体非线程安全**，靠外部锁兜底（`net.downloader` 用 `d.mu` 保护整个 `bufMap`）
- `stream.FileStream.CacheFullAndWriter` / `RangeRead`：**非线程安全**（`stream.go:64,176` 注释）；只有 `SeekableStream.RangeRead` 与 `RangeReadReadAtSeeker`（`sync.Map`）支持并发
- `op.multiReadCloser`：单消费者
- `op.directoryCache`：`RWMutex` + `dirtyFlags` 惰性重建 → **写缓存必须走 `UpdateObject`/`RemoveObject`，不要直接改 `objs`**
- `op` 的 Move/Copy/Rename：**无 singleflight**，并发写同一路径有竞态

**`http_range.Range` 是 struct，不是 interface**（`range.go:14`）

- `Length = -1` 是"不限长"哨兵，贯穿全库
- `(*Range).ContentRange()` **不可**用于 `Length < 0`（会算出非法头）
- `ApplyRangeToHttpHeader` **就地修改**传入的 `headerRef`，链式复用 header 时会被污染
- `ParseContentRange` 无法解析 `bytes */size` 通配形式

**路径规范化**

- 每个 op 入口第一步都是 `utils.FixAndCleanPath`（`path.go:19`：`\`→`/`，补前导 `/`，`path.Clean`）。**使用层不要传 OS 绝对路径**
- op 内部统一用 `stdpath`（纯 Unix 语义）拼接，**不要用 `path/filepath`**
- 两套缓存键语义不同：`op.Cache.Key = GetFullPath(MountPath, path)`（`cache.go:35`）vs `detailCache` 用 `GetActualMountPath`（去 `.balance` 后缀，`utils/balance.go:12`）。自定义缓存必须想清楚用哪个
- 根路径 `/` 在 `op.Get` 里需要驱动实现 `GetRooter` 或 `IRootId`/`IRootPath` 之一，否则直接报错（`fs.go:162`）；`Remove`/`Move`/`Rename` 显式禁止操作根
- `Link` 的缓存 key 含 `args.IP` 与 User-Agent（当 `LinkCacheMode` 含对应位）→ **使用层总是传不同 IP 会导致缓存几乎永不命中**

**错误约定**

- 一律用 `github.com/pkg/errors` 的 `WithStack` / `WithMessage` / `Wrapf`
- 判定用 `errs.IsObjectNotFound` / `IsObjectAlreadyExists` / `IsNotImplementError` / `IsNotSupportError` / `IsNotFoundError`，**不要字符串比较**
- 所有 `Is*` 辅助都套了 `pkgerr.Cause()`，**新写判定函数必须照做**，否则被包装后漏判
- `NotImplement` vs `NotSupport` 语义不同：前者"我不支持，请走通用路径"（op 据此回退到内置实现，如 `archive.go:163`）；后者"支持但结果要从别处取"（`archive.go:285` 据此改走 `ArchiveMeta.GetTree`）。**混用会破坏回退逻辑**
- `NewErr` 用 `"; "` 分隔原文与附加信息

**其它易踩点**

- `CheckStatus` 只在 op 入口检查一次，driver 内不重复检查
- `Remove` 幂等（不存在返回 nil）；`MakeDir` 幂等（目录已存在返回 nil，但**文件已存在返回 `"file exists"`**）
- `Put` 的 `NoOverwriteUpload` 会留下 `.openlist_to_delete` 临时文件（`fs.go:626`），失败时尝试 rename 回原名（失败仅记 error）
- 写操作返回的对象**可能为 nil**（未实现 `*Result` 变体的驱动），op 统一以 `Object{Mask: Temp}` 兜底 → 使用层若强依赖返回对象字段需自行处理
- `model.ListArgs.WithStorageDetails`（`args.go:17`）**在 op.list 中未被消费**
- `Copy` 的 `NoCopy` mask 检查在 `fs.go:523-525` 被**注释掉了**
- `UpdateStorage` 禁止改驱动类型（`storage.go:227`）；改 mountPath 会删缓存树 + 失效 details
- `ArchiveDecompress` 的 `lazyCache ...bool` 为真时不清缓存、不触发 hook
- `DeleteSettingItemByKey` 只允许删 `IsDeprecated()` 的项
- `CreateSSHPublicKey` 返回 `(error, bool)`，第二个值区分"格式错误"与"业务错误"，**不要只判 `err != nil`**
- `PageReq.Validate()` 是**静默修正**而非返回错误，与 `SearchReq.Validate()`（返回 error）行为不一致
- `model.Object` **没有 json tag**，序列化字段名是大写
- `op.initStorage` 带 `recover()`：Init 中 panic 被捕获，仍会 `Store` 进 `storagesMap` → **panic 的 storage 也会出现在挂载列表里**，只是 Status 不是 WORK，后续操作被入口 `CheckStatus` 拦下
- `initStorage` 顺序是 `Init → storagesMap.Store → SetStatus`（`storage.go:136-149`），**Init 期间该 storage 还不可见**
- `utils.WriteJsonToFile` 用 **0777** 权限写文件
- `utils.CopyDir` 用 `fmt.Println` 且**吞掉子树错误**
- `utils/ip.go ClientIP` 采信 `X-Forwarded-For` / `X-Real-Ip`（若暴露公网需在上层加信任边界）
- `utils/hash.go` 在**包变量初始化期**向 `name2hash` 等无锁 map 写入
- `utils.Random.String` 错误时 `panic`
- `conf.ContextKey` 是 `int8` 且 `iota` 预留了一个 `_`，加到 127 会溢出
- `cron.Cron.Stop()` 通过 `close(ch)` 停止，**调用两次会 panic**；停止不等待 `f()` 结束
- `conf` 包**不能** import `op` / `db`，否则成环（`op/driver.go:7` 已引用 `conf.TypeString`）

---

## 12. 与 OpenList 路径对照

| OpenList | cloudfs-139 |
|---|---|
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

> 需要新端点时，可先在 OpenList 源码（`/home/super/Downloads/code/OpenList-4.2.5`，见 DEV.md §6）找对应实现，复制后改写 import：`github.com/OpenListTeam/OpenList/v4/internal/*` → `github.com/milin2436/cloudfs-139/cloudfso/*`，`.../drivers/base` → `.../cloudfso/driver`。

**为什么是 `cloudfso/` 而不是 `internal/`**：Go 的 `internal` 包只能被同一 module root 下的包导入。把 OpenList 的 `internal/` 重新打包为**普通包** `cloudfso/`，SDK 和 CLI 才能自由 import。将来拆独立 module 时这一点仍然安全。

---

## 13. 建议的后续工作清单

### 已完成（2026-10-12）

- [x] 修复编译阻塞：`weak_cache.go` 加 `//go:build go1.27`；`GOPROXY=https://goproxy.cn,direct`
- [x] 建使用层初始化骨架（§7 七步 → 实际落在 `Client.onceInit`，5 步 + 循环 `op.CreateStorage`）
- [x] 建 SDK 包：`NewClientFromFile` / `NewClient` / `Init` / `List` / `Stat` / `Download`(`Get`) /
      `Upload`(`Put`) / `PutURL` / `MakeDir` / `Remove` / `Move` / `Rename` / `Copy` /
      `Space`(`op.GetStorageDetails`) / `UserInfo`(类型断言 yun139) / `Other`
- [x] 建 CLI（cobra v1.10.2 已在依赖里；`atMost()` 规避缺失的 `MaximumArgs`）
- [x] 补 SDK 单测（`config_test.go` 12 个 + `dsn_test.go` 1 个，共 13 个用例）
- [x] 修重写引入的 `sqliteDSN` bug：`file::memory:...` 会被 SQLite 当成磁盘文件名，
      在 cwd 写出含 Authorization token 的 `:memory:cloudfs`，且跨进程撞 `mount_path` 唯一约束
      → 改为 `file:cloudfs?mode=memory&cache=shared`，`TestSQLiteDSNIsInMemoryAndShared` 回归锁死
- [x] 修文档落差：README 的 `driver` 拼写、`RootID`→`RootFolderID`、`Upload` 签名；
      DEV.md 的 Go 版本 / GOPROXY / OpenList 路径 / 测试配置 / init 流程
- [x] 新增 DEV.md §4.12/§4.13/§4.14 三个坑（DSN、struct tag 缺省、`op.List` 无 Path），
      §5.2 端到端测试改写为真实账号流程
- [x] 分享（外链）功能三层实现：`drivers/yun139/share.go` + `sdk/cloudfs/share.go` +
      `cmd/cloudfs/share.go`，新增 `share` / `shares` / `unshare` 三个命令。真机验证：
      自建空目录 → 分享（无码永久 / 加码 1 天）→ `shares` 列出 → `unshare` 撤销 → 删除。
      见 §6.9 与 DEV.md §3.5 / §4.15
- [x] 修 `.gitignore` 的裸 `cloudfs` 模式（它会把 `sdk/cloudfs/` 与 `cmd/cloudfs/`
      整个排除在版本控制之外），并阻止编译产物 `cmd/cloudfs/cloudfs` 进仓库。见 DEV.md §4.11
- [x] `space` 从"必须先有 `user_domain_id`"改成按云盘类型分项的表格显示：删掉
      `GetDetails` 的前置 guard 和 SDK 里对应的兜底报错，新增 `GetDiskQuota` /
      `Client.Quota` / `quotaTable`。同时钉死配额契约（空 body 即可、单位 MB、
      没有目录级配额），并定位到上传被拒的原因 —— 本账号 20 GiB 配额已超额约 198 GiB。
      见 §6.10 与 DEV.md §3.6 / §4.17

### 仍待办（按依赖顺序）

- [x] **P0 端到端联调**：真实账号跑通 `whoami`/`ls`/`mkdir`/`mv`/`rename`/`cp`/`rm`/
      `Stat`/`Download`（前 256KiB）。联调中修掉三个真 bug：
      `sqliteDSN` 落磁盘文件、`addition.type` 缺省导致 `NotImplement`、
      `toFile` 用 `o.GetPath()` 导致所有条目的 `path` 塌缩成挂载点
- [ ] **上传与配额**：账号已满（`00010012 资源配额不足`），上传与整文件下载
      尚无成功样本。超额程度已量化：20 GiB 配额、已用 222,973 MB ≈ 217.7 GiB
      （个人云 192.3 GiB + 家庭云 25.5 GiB），`freeDiskSize` 为 −202,493 MB。
      腾出空间后补一次 `put` → `get` → sha256 比对
- [ ] 修 `util.go:914` 的 `panic(err)` → 返回 error（一次字段格式异常会崩掉整个进程）
- [ ] 修 `util.go:1167` 全局 RedirectPolicy 污染（`defer` 恢复，或改局部 `resty.NewClient()`）
- [ ] 给 `Yun139` 加 `sync.RWMutex` 保护 `Authorization` / `ref` / `cron`
- [ ] 补框架层单测 —— 至少给 `calSign` / `aesCbcEncrypt` / `FixAndCleanPath` /
      `GetStorageAndActualPath` 写单测（这些是纯函数，最好测）
- [ ] 接 `RegisterTool` 恢复压缩包功能，或删除整个 `archive/tool/helper.go` 死代码
- [ ] 决定是否保留 OpenList 多租户残留（user/meta/setting_item/search_node/sharing/sshkey
      五张表 + 对应 op 包）。若不要，`db.AutoMigrate` 的参数列表一起精简
- [ ] CLI 补：递归上传目录、`get` 下载整目录、`ls --json` 的排序与分页参数
- [ ] `go.mod` 的 `go 1.25.0` 与工具链 1.26.5 是否对齐，随 1.27 恢复后一并确认
