# TuneBridge real-device verification

These checks are required before treating TuneBridge as a long-term personal
library. They have not been performed by the automated test suite.

## Before testing

1. Configure a private HTTPS endpoint, Basic Auth, `TUNEBRIDGE_NETEASE_API_BASE_URL`, and a locally generated 32-byte Base64 session key.
2. Complete QR login through the protected login API, then confirm `/readyz` returns 200.
3. Keep server logs open. Inspect `request_id`, method, virtual path, Range,
   response status, and latency only. Do not enable logs containing credentials,
   cookies, or signed upstream URLs.

## iPhone OnePlayer

1. Add the HTTPS WebDAV URL and Basic Auth in OnePlayer.
2. Browse `网易云` → `我喜欢的音乐`, then `我的歌单` and a real playlist.
3. Confirm titles, artists, albums, file extensions, and no broken duplicate names.
4. Play one song, seek to 50%, move to the next song, return, then play the
   first song again. The second full request should be served from local cache.
5. Open lyrics and test the same-basename `.lrc` behavior.
6. Use `https://host/search` on Safari, add a search result, refresh OnePlayer,
   and confirm it appears under `搜索结果`.
7. Confirm cover behavior: request logs show whether OnePlayer reads embedded
   tags, sidecars, directory artwork, or only its own downloaded metadata.

## iPad

Repeat browse, cover/metadata, lyrics, seek, background playback, cache hit,
and any local-download workflow. **NEEDS REAL DEVICE VERIFICATION:** iPad may
not make the same WebDAV request sequence as iPhone.

## Apple Watch

1. Confirm whether Watch OnePlayer first requires iPhone configuration.
2. Check visibility of the WebDAV server, `网易云`, playlists, and track browse.
3. Test Wi-Fi playback, and cellular playback if supported.
4. Test seek, next/previous, cover, lyrics, Watch download, and offline play.

Every Watch result is **NEEDS REAL DEVICE VERIFICATION** until recorded on the
actual device. TuneBridge must not claim standalone Watch support merely from
iPhone/iPad behavior.
