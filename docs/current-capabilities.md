# TuneBridge 当前能力清单与系统现状 (Current Capabilities Inventory)

本文档基于对当前仓库源码、配置、数据库迁移以及测试套件的实际检查，客观梳理 TuneBridge 项目的真实功能实现边界。

---

## 1. 已实现且当前服务可调用

本节列出已在服务入口（`cmd/tunebridge/main.go`）完成装配并在运行时 HTTP 服务（`internal/app/server.go`）中实际注册、可对外接收请求的能力。

### 1.1 服务健康与就绪检查端点
- **提供端点**：
  - `GET /healthz`：基础存活检查，直接返回 HTTP 200 `{"status":"ok"}`。
  - `GET /readyz`：数据库就绪检查，向 SQLite 发起 `db.PingContext`。连通正常返回 HTTP 200 `{"status":"ready"}`；数据库故障返回 HTTP 503 `{"status":"not_ready"}`。
- **安全与认证要求**：公开端点，无需 HTTP Basic Auth 认证。
- **测试验证**：`internal/app/server_test.go` (`TestHealthAndReadyAreAvailableWithoutWebDAVCredentials`) 验证了未携带凭证时的探针可用性。

### 1.2 WebDAV 受保护基础骨架服务
- **提供端点与协议**：挂载于根路径 `/`，只读 WebDAV 处理器。
- **配置与安全要求**：
  - **强制 HTTP Basic Auth**：启动时强制要求环境变量 `TUNEBRIDGE_WEBDAV_USERNAME` 与 `TUNEBRIDGE_WEBDAV_PASSWORD`，**严格禁止匿名 WebDAV**（未配置则服务拒绝启动）。未认证或凭据错误统一返回 HTTP 401 Unauthorized (`WWW-Authenticate: Basic realm="TuneBridge WebDAV", charset="UTF-8"`）。
- **请求方法与行为边界**：
  - `OPTIONS`：返回 HTTP 204 No Content，声明头部 `DAV: 1` 与 `Allow: OPTIONS, PROPFIND, GET, HEAD`。
  - `PROPFIND`：支持 `Depth: 0` 和 `Depth: 1`（请求 `Depth: infinity` 返回 HTTP 403 Forbidden）。返回标准 XML Multi-Status。当前服务启动时**仅装配了硬编码的静态目录骨架**（`webdav.BootstrapLibrary()`），包含以下只读集合：
    - `/`
    - `/网易云`
    - `/网易云/我喜欢的音乐`
    - `/网易云/我的歌单`
    - `/网易云/每日推荐`
    - `/网易云/搜索结果`
  - `HEAD`：获取指定虚拟集合的元数据响应头（HTTP 200）。
  - `GET`：请求目录集合返回 HTTP 405；请求文件资源硬编码返回 HTTP 503 Service Unavailable（`stream resolver is not configured`）。
  - 写方法（`PUT`, `DELETE`, `MKCOL` 等）：统一拦截并返回 HTTP 405 Method Not Allowed。
- **与真实播放的明确边界**：
  - **当前仅为 WebDAV 协议骨架与固定目录视图，尚未接入任何真实音频流传输，无法供 OnePlayer 进行真正的音频点播或离线回放**。
- **测试验证**：`internal/webdav/handler_test.go` 覆盖了认证拦截、只读方法声明、`Depth: 0/1` 的 XML 转义与 URL 编码、以及非法 Depth 拦截。

### 1.3 可选网易云扫码登录流程与会话持久化
- **挂载条件与配置要求**：
  - 默认不启用。仅当环境变量 `TUNEBRIDGE_NETEASE_API_BASE_URL` 配置了合法的 HTTP/HTTPS 绝对 URL 时激活挂载。
  - 激活时**强制要求**提供 `TUNEBRIDGE_SESSION_ENCRYPTION_KEY`（必须为 Base64 编码的 32 字节密钥），否则配置校验失败直接退出。
  - **上游依赖说明**：TuneBridge **不提供任何公开默认的网易云 API 服务端点与密钥**，完全依赖用户自选或自建的兼容 [NeteaseCloudMusicApi](https://github.com/Binaryify/NeteaseCloudMusicApi) 服务。
- **提供端点**（受与 WebDAV 相同的 HTTP Basic Auth 保护）：
  - `POST /api/sources/netease/login/qr`：向自建上游请求生成二维码 Key 与图像。成功返回 HTTP 201 Created，载荷包含 `key`、`url`、`image_data`；上游异常返回 HTTP 502 Bad Gateway；非 POST 请求返回 HTTP 405。
  - `GET /api/sources/netease/login/qr/{key}`：向自建上游轮询指定 Key 的扫码状态。映射状态包括 `waiting` (801)、`awaiting_confirmation` (802)、`authorized` (803)、`expired` (800)；Key 为空或非法字符返回 HTTP 400；上游通信故障返回 HTTP 502；非 GET 请求返回 HTTP 405。
- **持久化与安全保护**：
  - 当状态为 `authorized` 时，服务端自动提取会话 Cookie，使用 AES-GCM 算法（每次随机生成 12 字节 Nonce）加密后写入 SQLite 的 `source_sessions` 表（`encrypted_payload` BLOB 字段）。
  - **会话安全性保证**：API 端点响应严格仅返回 `{"status":"authorized"}`，**绝不通过 HTTP 响应暴露 Cookie、Token 明文或解密密钥**；日志亦脱敏不打印会话载荷。
- **与真实账号联调的明确边界**：
  - **当前仅验证了扫码状态机与加密落库流程，所有测试均针对 Mock 上游执行，不代表真实网易云账号及公网环境下的验证**。
- **测试验证**：`internal/api/netease_login_test.go`、`internal/app/server_test.go`、`internal/session/repository_test.go` 覆盖了扫码创建、状态轮询、Basic Auth 鉴权、AES-GCM 加密落库及禁止泄露 Cookie 的断言。

### 1.4 配置与底层数据库基础设施
- **配置系统 (`internal/config`)**：支持从环境变量加载配置，校验绝对路径、缓存上限（默认 10GB）、歌单 TTL（默认 5m）、日推 TTL（默认 1h）及网易云配置依赖完整性。
- **数据库迁移与连接 (`internal/database`)**：采用 `modernc.org/sqlite`（纯 Go、无 CGO 依赖），启动时自动运行嵌入式 SQL 迁移 `001_foundation.sql`，管理 `schema_migrations`、`sources`、`source_sessions`、`tracks`、`cache_entries` 数据表。
- **测试验证**：`internal/config/config_test.go`、`internal/database/database_test.go`。

---

## 2. 已实现代码但尚未接入可用播放链路

本节列出代码已编写并通过单元测试，但**尚未在 `main.go` 或运行时 WebDAV 目录中完成组装**的孤立能力模块。

### 2.1 网易云业务适配器 (`internal/source/netease/adapter.go`)
- **已实现的代码能力**：
  - 完整实现了 `source.MusicSource` 接口定义的方法：
    - `UserProfile`：读取持久化会话后调用 `/login/status` 获取用户 ID 与昵称。
    - `LikedTracks`：调用 `/likelist` 获取我喜欢的音乐 ID 列表，并分批调用 `/song/detail` 装配完整音轨元数据。
    - `Playlists`：调用 `/user/playlist` 获取用户歌单列表。
    - `Playlist`：调用 `/playlist/detail` 与分页 `/playlist/track/all` 拉取指定歌单全量歌曲。
    - `DailyRecommendations`：调用 `/recommend/songs` 获取每日推荐歌曲。
    - `SearchTracks`：调用 `/cloudsearch` 进行单曲关键词检索。
    - `Track` / `Cover`：根据单曲 ID 获取元数据与封面信息。
    - `Lyrics`：调用 `/lyric` 获取包含原歌词、翻译歌词与罗马音歌词的数据结构。
    - `ResolveStream`：根据指定的音质档位（`lossless`, `exhigh`, `higher`, `standard`）调用 `/song/url/v1` 解析音频直链 URL、预估大小、文件扩展名与编码码率。
- **与虚拟库行为的割裂现状（未接入点）**：
  - **Adapter 能力 ≠ 运行时虚拟库行为**：在 `cmd/tunebridge/main.go` 中，仅初始化了 `netease.Client` 并传给扫码处理器；`netease.Adapter` 根本没有被实例化，也没有注入到 WebDAV 库中。
  - 运行时 WebDAV 仍然绑定在固定写死的 `BootstrapLibrary()` 上，无法将上述适配器拉取到的网易云用户歌单、每日推荐或歌曲动态映射为 WebDAV 目录节点与文件。
- **测试验证**：`internal/source/netease/adapter_test.go` 针对 Mock 服务器验证了流解析、歌词字段完整性、未授权拒绝、歌单详情与分页加载。

### 2.2 HTTP Range 分片请求解析器 (`internal/stream/range.go`)
- **已实现的代码能力**：
  - 实现了基于 RFC 7233 的单 Byte-Range 头部解析（支持 `bytes=0-`、`bytes=1000-`、`bytes=1000-2000`、`bytes=-250` 等形态）。
  - 严格防御与校验：拒绝多 Range 请求（multipart）、拒绝语法错误、拒绝超出文件已知大小的不可满足区间。
- **未接入点**：
  - 当前 WebDAV 的 `GET` 方法在命中非目录资源时直接返回 503，尚未调用 `stream.ParseRange`，未接入任何音频流的分片读取、206 Partial Content 响应或 Seek 跳转管道。
- **测试验证**：`internal/stream/range_test.go` 对各类单区间及非法边界情况进行了完整的单元测试。

### 2.3 虚拟文件名与侧车文件命名规范器 (`internal/library/filename.go`)
- **已实现的代码能力**：
  - `AudioFilename`：根据音轨标题与艺术家清洗拼接展示文件名，如 `晴天 - 周杰伦.flac`。
  - `SanitizeFilename`：过滤特殊字符（`/\\:?*"<>|` 与控制字符）并规范化空白。
  - `truncateFilename`：在 240 字节预算内安全截断，防止破坏 UTF-8 多字节字符。
  - `CollisionFilename`：同名冲突时追加 `[source-trackID]` 稳定标识。
  - `LyricsFilename` / `CoverFilename`：确保 `.lrc` 与 `.cover` 文件基名严格与音频文件保持一致。
- **未接入点**：
  - 当前 WebDAV 骨架中没有任何曲目节点，虚拟树尚未生成任何文件项，上述命名算法未在实际 WebDAV 树构建中被触发调用。
- **测试验证**：`internal/library/filename_test.go` 覆盖了非法字符清洗、UTF-8 边界截断、同名歌词配对与冲突消歧逻辑。

### 2.4 数据库歌曲与缓存元数据表结构 (`001_foundation.sql`)
- **已定义的数据模型**：
  - `tracks` 表：包含来源 ID、上游音轨 ID、标题、艺术家 JSON、专辑信息、封面 URL 等字段。
  - `cache_entries` 表：包含缓存键、字节大小、MIME 类型、本地路径、最后访问时间及 LRU 索引。
- **未接入点**：
  - 业务层尚未编写针对 `tracks` 的元数据持久化读写仓储，也未编写结合 `cache_entries` 的音频缓存管理与 LRU 清理逻辑。

---

## 3. 尚未实现

本节列出项目中完全缺失的功能链路以及尚未进行实际验证的事项。

### 3.1 尚未实现的功能链路
1. **真实的 OnePlayer WebDAV 播放与流式链路**：
   - **动态虚拟音乐库驱动**：尚未实现将 `netease.Adapter` 数据动态映射为 WebDAV 树的 Provider。`/网易云` 内部各目录无法根据登录用户动态展开歌单与单曲。
   - **音频流代理与分片响应**：WebDAV `GET` 尚未实现流式反向代理或直链重定向，未返回 HTTP 206 Partial Content，不支持播放器 Seek。
   - **侧车歌词与封面暴露**：尚未在 WebDAV 目录中动态挂载与音频同名的 `.lrc` 资源或封面资源。
2. **本地音频缓存引擎与 LRU 淘汰**：
   - 虽然配置了 `TUNEBRIDGE_AUDIO_CACHE_DIR` 并在启动时创建了目录，但尚未实现边下边播的音频缓存写入机制。
   - 尚未实现基于 `TUNEBRIDGE_CACHE_MAX_BYTES` 的容量监控与基于 SQLite `cache_entries` 的 LRU 自动清理机制。
3. **元数据持久化同步与 TTL 缓存机制**：
   - 歌单元数据与每日推荐的 TTL（`PlaylistTTL`, `DailyRecommendationTTL`）尚未在业务层接入；缺少将远端歌单按需落盘至 SQLite `tracks` 表的同步器。
4. **多音源抽象扩展**：
   - 目前仅编写了网易云单个数据源的代码，其他数据源或本地曲库映射尚未支持。
5. **用户交互前端**：
   - 扫码登录目前仅为 REST API，尚未提供便于在移动端/浏览器端直接展示二维码的 Web 控制台界面。

### 3.2 尚未验证的事项 (Unverified Items)
- **缺乏真实设备与客户端验证 (Needs Real Device Proof)**：
  - 尚未在真实的 iPhone、iPad 或 Apple Watch 上的 OnePlayer 客户端中配置连接 TuneBridge WebDAV 服务。
  - 尚未验证 OnePlayer 远端索引真实网络加载性能与目录兼容性。
  - 尚未验证 OnePlayer 对同名 `.lrc` 歌词文件及内嵌/侧车封面的实际探测请求行为（参见 `docs/oneplayer-compatibility.md`）。
  - 尚未验证真实设备上的缓冲播放、拖拽 Seek、锁屏控制及离线下载功能。
- **缺乏真实网易云账号与线上 API 验证 (Needs Real Account & Live API Verification)**：
  - 现有 Adapter 和登录测试完全基于本地 HTTP Mock 服务，未连接真实的 `NeteaseCloudMusicApi` 实例。
  - 尚未在真实网易云账号下验证扫码授权全流程、Cookie 长期有效性及自动刷新。
  - 尚未验证真实网易云会员/版权保护曲目的解析可用性及不同音质直链的实际有效性。
- **缺乏生产容器化长期运行验证 (Needs Live Container Proof)**：
  - 虽有 `Dockerfile` 与 `docker-compose.yml`，但尚未在生产宿主机或容器集群中进行长时间运行、数据卷写权限及资源开销实测。

---

## 短期事实性演进优先级 (Next Priorities)

1. **构建动态虚拟音乐库驱动 (Virtual Library Provider)**：
   将已实现的 `netease.Adapter` 与 WebDAV 树连接，替代当前的写死静态 `BootstrapLibrary()`，使已登录会话能够动态向 WebDAV 暴露用户歌单与歌曲列表。
2. **打通 WebDAV 音频流分片播放 (Streaming & Range Support)**：
   移除 WebDAV `GET` 的 503 阻断，结合 `internal/stream/range.go` 实现对上游音频流的反向代理与 HTTP 206 Partial Content 支持，支持基本点播与 Seek 拖拽。
3. **落地本地音频流缓存与 LRU 清理 (Audio Cache Pipeline)**：
   利用 `AudioCacheDir` 与 SQLite `cache_entries` 表，实现音频流本地缓存落盘、重复请求命中与容量超限自动清理。
4. **进行真实账号与真实 iOS OnePlayer 设备端到端联调**：
   对接真实的 NeteaseCloudMusicApi 服务完成实机扫码，并在 iOS OnePlayer 客户端实测 WebDAV 挂载、歌曲扫描、音频播放、Seek 以及歌词/封面呈现。
