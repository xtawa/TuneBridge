# TuneBridge real-device verification

These checks are required before treating TuneBridge as a long-term personal
library. They have not been performed by the automated test suite.

## Before testing

1. Deploy behind HTTPS (for example `https://music.example.com/`) and set Basic Auth, `TUNEBRIDGE_NETEASE_API_BASE_URL`, and a locally generated 32-byte Base64 session key. Never use public HTTP.
2. Set `TUNEBRIDGE_COMPAT_TRACE=true`, restart, complete QR login through the protected API, then confirm `GET /healthz` and `GET /readyz` return 200.
3. Keep structured logs available. Trace data is read from the Basic-Auth-protected `GET /api/debug/recent-requests`; it contains no Authorization, Cookie, URL query, or upstream URL.

## iPhone OnePlayer

1. Open `https://music.example.com/search` in Safari. Authenticate, search a song, add it, delete it, clear it, then add it again. Expected: no cross-origin/auth error; a refresh later shows it under `搜索结果`.
2. In OnePlayer add `https://music.example.com/` as WebDAV with the same Basic Auth. Expected root: `网易云`.
3. Browse `网易云` and verify exactly `我喜欢的音乐`、`我的歌单`、`每日推荐`、`搜索结果`; then open a real playlist. Expected: real track list, readable Unicode, stable duplicate names.
4. Start a track and record time-to-first-audio (target normally below two seconds on a healthy broadband connection). If it fails, capture the matching `request_id` trace rows.
5. At about 30 seconds seek to 50%. Expected: playback continues near target position and trace has a new `Range` with HTTP 206, not a restart at zero.
6. Play a complete track, then replay it. Expected: same headers/ETag and cache-hit evidence in server logs; no second full upstream download.
7. Inspect track list, Now Playing, and album view for artwork. Then inspect trace: record whether OnePlayer asks only for audio, a sidecar, or another resource. Do not infer a cover strategy without this evidence.
8. Open lyrics. If absent, look in trace for `HEAD`/`GET` of the matching `.lrc`: no request means OnePlayer likely does not discover remote WebDAV LRC; a request without display requires MIME/naming/content evidence.

## iPad

Repeat all iPhone steps, plus background playback and any local-download
workflow. Export `/api/debug/recent-requests` after the scan. **NEEDS REAL
DEVICE VERIFICATION:** iPad may not make the same WebDAV request sequence.

## Apple Watch

1. Confirm whether configuration must first be created on iPhone and synced.
2. Check visibility of WebDAV, `网易云`, playlists, and browse.
3. Test Wi-Fi streaming, cellular streaming if available, seek, next/previous,
   artwork, lyrics, Watch download, and offline play.
4. Compare timestamp/User-Agent/request timing in the trace to determine whether
   traffic is Watch → TuneBridge, Watch → iPhone → TuneBridge, or iPhone
   download/sync. Do not persist client IP addresses for this purpose.

Every Watch result is **NEEDS REAL DEVICE VERIFICATION** until recorded on the
actual device. TuneBridge must not claim standalone Watch support merely from
iPhone/iPad behavior.
