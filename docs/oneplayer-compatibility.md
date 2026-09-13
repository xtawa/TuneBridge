# OnePlayer Compatibility Contract

## Confirmed behavior

OnePlayer documents both WebDAV remote-library access and indexing remote music
into its song, album, and artist views. Its documentation also states that it
can read supported embedded covers and lyrics. The FAQ documents synchronized
lyrics as an `.lrc` file with the same basename in the same directory as the
audio file.

## TuneBridge behavior

- A track has a stable `source_id:track_id` identity; display filenames never
  serve as database keys.
- `Track.Cover` retains the upstream cover URL and MIME type. TuneBridge will
  not relabel an audio stream as if artwork were embedded when it is not.
- `Track.Lyrics` retains original, translated, and romanized text separately.
- The only sidecar naming rule is derived from the actual audio filename:
  `晴天 - 周杰伦.flac` maps to `晴天 - 周杰伦.lrc`. Sanitization, Unicode, and
  collision handling apply to the audio name first, so paired files cannot
  drift apart.
- Audio extension, MIME type, and actual upstream codec are one contract.
  Cover/lyrics metadata must never cause a false `.flac` name or
  `audio/flac` header for MP3 bytes.

## Verification gate

The current public sources do not prove that OnePlayer discovers dynamically
served `.lrc` files or folder artwork while indexing a remote WebDAV library.
Accordingly, TuneBridge does not yet expose a speculative cover sidecar or
claim automatic remote LRC discovery. Before enabling that behavior, test a
real iPhone, iPad, and Apple Watch against a virtual audio file plus its
same-name LRC resource and record the WebDAV request sequence. Embedded audio
tags can be relied on only after the upstream stream format has been inspected.

## Required device checks

1. Add the protected WebDAV endpoint and index a directory containing a track.
2. Verify the resulting album/track cover and record whether OnePlayer fetched
   embedded bytes, a folder image, or another path.
3. Verify the same-name LRC resource and each original/translation/romanized
   presentation option.
4. Repeat browse, stream, seek, download, and offline playback on iPhone,
   iPad, and Apple Watch.

Until then, these are **NEEDS REAL DEVICE VERIFICATION**, not completed
compatibility claims.
