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
| `TUNEBRIDGE_PLAYLIST_TTL` | 否 | `5m` | 歌单元数据缓存时间。 |
| `TUNEBRIDGE_DAILY_RECOMMENDATION_TTL` | 否 | `1h` | 每日推荐元数据缓存时间。 |
| `TUNEBRIDGE_NETEASE_API_BASE_URL` | 否 | 无 | 用户自选或自建的 [NeteaseCloudMusicApi](https://github.com/Binaryify/NeteaseCloudMusicApi) 兼容服务绝对 URL。**无任何公共默认地址**。 |
| `TUNEBRIDGE_SESSION_ENCRYPTION_KEY` | 条件必填* | 无 | Base64 编码的 32 字节密钥，用于 AES-GCM 会话加密。*若配置了 `TUNEBRIDGE_NETEASE_API_BASE_URL` 则**必须**提供。 |

## 网易云扫码登录对接

TuneBridge 支持可选的网易云音乐扫码登录功能。该功能默认关闭，仅在配置了 `TUNEBRIDGE_NETEASE_API_BASE_URL` 与 `TUNEBRIDGE_SESSION_ENCRYPTION_KEY` 时启用。

### 1. 配置前提与密钥要求

- **上游服务地址**：`TUNEBRIDGE_NETEASE_API_BASE_URL` 必须指向用户自选或自建的 NeteaseCloudMusicApi 兼容服务，TuneBridge **不提供任何公共默认端点**。
- **会话加密密钥**：`TUNEBRIDGE_SESSION_ENCRYPTION_KEY` 必须可解码为 32 字节（256 位，用于 AES-GCM）。可通过以下命令生成：
  ```bash
  openssl rand -base64 32
  ```
  *(注：切勿在生产环境中使用弱密钥或公开示例密钥。)*

### 2. 扫码登录接口

启用后，TuneBridge 会注册两个 API 路由。这两个路由均受 **HTTP Basic Auth** 保护（与 WebDAV 使用相同的用户名与密码，Realm: `TuneBridge`）：

#### `POST /api/sources/netease/login/qr`
- **说明**：向自建上游申请二维码 Key 并生成二维码。
- **响应 (`201 Created`)**：
  ```json
  {
    "key": "example-unikey",
    "url": "https://music.163.com/login?codekey=...",
    "image_data": "data:image/png;base64,..."
  }
  ```
- **错误**：上游异常返回 `502 Bad Gateway`；非 POST 请求返回 `405 Method Not Allowed`。

#### `GET /api/sources/netease/login/qr/{key}`
- **说明**：轮询指定 `{key}` 的扫码认证状态。
- **响应 (`200 OK`)**：
  ```json
  {
    "status": "waiting"
  }
  ```
- **状态值含义**：
  - `waiting`：等待用户扫码（上游状态码 801）。
  - `awaiting_confirmation`：已扫码，等待用户在手机端确认授权（上游状态码 802）。
  - `authorized`：授权成功（上游状态码 803）。服务端将自动获取会话 Cookie、加密并存入 SQLite。
  - `expired`：二维码已过期（上游状态码 800）。
- **错误**：Key 为空或无效（如包含斜杠）返回 `400 Bad Request`；上游通信异常返回 `502 Bad Gateway`；非 GET 请求返回 `405 Method Not Allowed`。

### 3. 会话凭据安全与存储机制

- **AES-GCM 加密存储**：当扫码状态为 `authorized` 时，获取到的 Session Cookie 将使用 `TUNEBRIDGE_SESSION_ENCRYPTION_KEY` 经由 AES-GCM 算法加密，持久化到 SQLite 数据库的 `source_sessions` 表中。
- **API 绝不暴露 Cookie**：`GET /api/sources/netease/login/qr/{key}` 接口只返回 `{"status":"authorized"}`，**绝不通过 API 返回 Cookie 或会话明文**，避免凭据外泄。

## 运行示例

### 使用 Docker Compose

```bash
export TUNEBRIDGE_WEBDAV_USERNAME="your_username"
export TUNEBRIDGE_WEBDAV_PASSWORD="your_password"

# 可选：启用网易云扫码登录
# export TUNEBRIDGE_NETEASE_API_BASE_URL="http://your-netease-api:3000"
# export TUNEBRIDGE_SESSION_ENCRYPTION_KEY="<base64-encoded-32-byte-key>"

docker compose up -d
```

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
