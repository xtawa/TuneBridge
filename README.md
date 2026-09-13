# TuneBridge

TuneBridge 是一个专为离线音乐播放器（如 OnePlayer）设计的 WebDAV 音乐桥接服务。

> [!NOTE]
> 当前阶段提供 WebDAV 虚拟目录结构、健康检查与可选的扫码登录对接，**尚未实现音乐库回放与音频流解析（Library Playback / Audio Streaming）**。请求音频流内容将返回 `503 Service Unavailable`。

## 特性

- **严格的 WebDAV 基础认证**：不提供匿名访问，启动时强制校验用户名与密码。
- **服务健康与就绪检查**：提供 `/healthz` 与 `/readyz` HTTP 探针。
- **可选网易云扫码登录**：支持通过外部兼容服务进行二维码登录与状态轮询。
- **AES-GCM 加密存储**：登录成功的 Session Cookie 会以 AES-GCM 算法加密保存在 SQLite 中，且**绝不通过 API 返回**。

## 环境变量说明

| 环境变量 | 必填 | 默认值 | 说明 |
|---|---|---|---|
| `TUNEBRIDGE_WEBDAV_USERNAME` | **是** | 无 | WebDAV 与 API 路由的 HTTP Basic 认证用户名（不支持匿名）。 |
| `TUNEBRIDGE_WEBDAV_PASSWORD` | **是** | 无 | WebDAV 与 API 路由的 HTTP Basic 认证密码（不支持匿名）。 |
| `TUNEBRIDGE_DATA_DIR` | 否 | `/data` | 持久化数据与缓存根目录。 |
| `TUNEBRIDGE_LISTEN_ADDRESS` | 否 | `:8080` | HTTP 服务监听地址。 |
| `TUNEBRIDGE_DATABASE_PATH` | 否 | `$TUNEBRIDGE_DATA_DIR/tunebridge.db` | SQLite 数据库路径。 |
| `TUNEBRIDGE_AUDIO_CACHE_DIR` | 否 | `$TUNEBRIDGE_DATA_DIR/cache/audio` | 音频文件缓存目录。 |
| `TUNEBRIDGE_CACHE_MAX_BYTES` | 否 | `10737418240` (10 GB) | 音频缓存上限字节数。 |
| `TUNEBRIDGE_ARTWORK_CACHE_DIR` | 否 | `$TUNEBRIDGE_DATA_DIR/cache/artwork` | 封面反代缓存目录。 |
| `TUNEBRIDGE_ARTWORK_CACHE_MAX_BYTES` | 否 | `1073741824` (1 GB) | 封面缓存上限字节数。 |
| `TUNEBRIDGE_PREFERRED_QUALITY` | 否 | `lossless` | `lossless`、`exhigh`、`higher` 或 `standard`；只使用账号实际可取的表示。 |
| `TUNEBRIDGE_LYRICS_MODE` | 否 | `original_translation` | `original`、`original_translation` 或 `original_romanized`。 |
| `TUNEBRIDGE_COMPAT_TRACE` | 否 | `false` | 启用 200 条内存兼容性 trace；通过受认证的 `/api/debug/recent-requests` 读取。 |
| `TUNEBRIDGE_PLAYLIST_TTL` | 否 | `5m` | 歌单元数据缓存时间。 |
| `TUNEBRIDGE_DAILY_RECOMMENDATION_TTL` | 否 | `1h` | 每日推荐元数据缓存时间。 |
| `TUNEBRIDGE_NETEASE_API_URL` | 否 | 无 | 可选外部后端 [NeteaseCloudMusicApi](https://github.com/Binaryify/NeteaseCloudMusicApi) 兼容服务绝对 URL（别名：`TUNEBRIDGE_NETEASE_API_BASE_URL`）。未配置时默认使用 TuneBridge 原生网易云直连能力。 |
| `TUNEBRIDGE_SESSION_ENCRYPTION_KEY` | 否 | 自动生成 | Base64 编码的 32 字节密钥，用于 AES-GCM 会话加密。未配置时自动在 `$TUNEBRIDGE_DATA_DIR/session.key` 生成持久化密钥。 |

## 网易云原生登录与 Cookie 导入

TuneBridge 内置原生网易云扫码登录与 Cookie 导入能力，**默认无需额外部署任何外部 NeteaseCloudMusicApi 服务**。

### 1. 移动端登录配置页面 (`/setup/netease`)

通过浏览器或手机访问受 Basic Auth 保护的 `/setup/netease`，即可体验完整的登录交互：
- **实时二维码扫码**：自动生成网易云登录二维码，提供“打开网易云音乐 App 授权”直跳链接，状态机实时轮询（等待扫码、已扫码待确认、授权成功、自动过期提示）。
- **风控回退机制 (Cookie 导入 Fallback)**：当遇到网易云异地登录或二维码风控限制时，可展开备用面板，直接粘贴 `MUSIC_U` 或完整 Cookie。
- **二次账号验证**：无论是扫码登录还是 Cookie 导入，服务端均会在同一 Cookie Jar / 会话中调用网易云官方账号接口二次验证，确保凭据真实有效。
- **AES-GCM 加密落库**：验证通过后使用 AES-GCM 算法持久化到 SQLite `source_sessions` 表。

### 2. 登录与会话 API 端点

所有 API 均受 **HTTP Basic Auth** 保护（与 WebDAV 相同凭据）：

#### `GET /setup/netease`
- **说明**：移动端原生登录与凭据配置页面。

#### `POST /api/sources/netease/login/qr`
- **说明**：申请二维码 Key 并生成 Base64 PNG 二维码图像。
- **响应 (`201 Created`)**：
  ```json
  {
    "key": "example-unikey",
    "url": "https://music.163.com/login?codekey=...",
    "image_data": "data:image/png;base64,..."
  }
  ```

#### `GET /api/sources/netease/login/qr/{key}`
- **说明**：轮询指定 `{key}` 的扫码认证状态。状态包括 `waiting` (801)、`awaiting_confirmation` (802)、`authorized` (803)、`expired` (800)。
- **响应 (`200 OK`)**：
  ```json
  {
    "status": "waiting"
  }
  ```

#### `POST /api/sources/netease/login/cookie`
- **说明**：手动导入 `MUSIC_U` 或完整 Cookie 作为风控备用 fallback。
- **请求 (`POST`)**：
  ```json
  {
    "cookie": "MUSIC_U=xxxx..."
  }
  ```
- **响应 (`200 OK`)**：`{"status":"authorized"}`

#### `GET /api/sources/netease/status`
- **说明**：获取当前网易云会话状态（`{"logged_in": true/false}`）。

### 3. 会话凭据安全与隐私保护

- **严禁密码登录**：系统严格杜绝默认账号密码登录方案，消除凭据滥用风险。
- **零凭据泄露保障**：登录流程与状态接口严格仅返回状态标识（如 `{"status":"authorized"}`），**绝不在任何 HTTP 响应、HTML 页面或日志流中打印 Cookie、MUSIC_U、__csrf 等敏感凭据**。
- **AES-GCM 加密存储**：加密落库时采用独立随机 12 字节 Nonce，保障离线凭证高强度安全。

## 运行示例

> **安全要求：** WebDAV 使用 HTTP Basic Auth。公网部署必须通过 HTTPS
> 反向代理终止 TLS，绝不可将明文 HTTP Basic Auth 直接暴露到互联网。

### 使用 Docker Compose

```bash
export TUNEBRIDGE_WEBDAV_USERNAME="your_username"
export TUNEBRIDGE_WEBDAV_PASSWORD="your_password"

# 可选：启用网易云扫码登录
# export TUNEBRIDGE_NETEASE_API_BASE_URL="http://your-netease-api:3000"
# export TUNEBRIDGE_SESSION_ENCRYPTION_KEY="<base64-encoded-32-byte-key>"

docker compose up -d
```

升级前先备份 `/data/tunebridge.db` 与部署环境变量/密钥。音频与封面缓存可以
安全重建，不需要备份。升级时使用 `docker compose pull && docker compose up -d`
（本地构建镜像则改为 `docker compose build && docker compose up -d`）；不要删除
`tunebridge-data` volume。

登录完成后，WebDAV 顶层为 `网易云`。手机搜索页是受相同 Basic Auth
保护的 `/search`；它不会暴露网易云音频直链。

真实 OnePlayer / iPhone / iPad / Apple Watch 验收请看
[docs/REAL_DEVICE_TEST.md](docs/REAL_DEVICE_TEST.md)。

### 使用 Docker CLI

```bash
docker run -d \
  --name tunebridge \
  -p 8080:8080 \
  -v tunebridge-data:/data \
  -e TUNEBRIDGE_DATA_DIR=/data \
  -e TUNEBRIDGE_WEBDAV_USERNAME="your_username" \
  -e TUNEBRIDGE_WEBDAV_PASSWORD="your_password" \
  -e TUNEBRIDGE_NETEASE_API_BASE_URL="http://your-netease-api:3000" \
  -e TUNEBRIDGE_SESSION_ENCRYPTION_KEY="<base64-encoded-32-byte-key>" \
  tunebridge:latest
```

## 相关文档

- [运行时与部署架构 (docs/runtime-layout.md)](docs/runtime-layout.md)
- [OnePlayer 兼容性说明 (docs/oneplayer-compatibility.md)](docs/oneplayer-compatibility.md)
