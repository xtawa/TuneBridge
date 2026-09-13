package library

import (
	"strings"
	"testing"

	"github.com/xtawa/tunebridge/internal/model"
)

func TestAudioFilenamePreservesUnicodeAndNormalizesUnsafeCharacters(t *testing.T) {
	t.Parallel()
	got := AudioFilename(model.Track{Title: `晴天: live?`, Artists: []string{"周杰伦/朋友"}}, "FLAC")
	if got != "晴天 live - 周杰伦 朋友.flac" {
		t.Fatalf("got %q", got)
	}
}

func TestSidecarLyricsUsesExactAudioBase(t *testing.T) {
	t.Parallel()
	if got := LyricsFilename("晴天 - 周杰伦.flac"); got != "晴天 - 周杰伦.lrc" {
		t.Fatalf("got %q", got)
	}
}

func TestSidecarCoverUsesExactAudioBase(t *testing.T) {
	t.Parallel()
	if got := CoverFilename("晴天 - 周杰伦.flac"); got != "晴天 - 周杰伦.jpg" {
		t.Fatalf("got %q", got)
	}
}

func TestCollisionFilenameKeepsExtensionAndIdentity(t *testing.T) {
	t.Parallel()
	got := CollisionFilename("晴天 - 周杰伦.flac", model.TrackIdentity{SourceID: "netease", TrackID: "186016"})
	if got != "晴天 - 周杰伦 [netease-186016].flac" {
		t.Fatalf("got %q", got)
	}
}

func TestFilenameIsByteBoundedWithoutBreakingUTF8(t *testing.T) {
	t.Parallel()
	got := AudioFilename(model.Track{Title: strings.Repeat("晴", 300)}, "mp3")
	if len(got) > maxFilenameBytes || !strings.HasSuffix(got, ".mp3") {
		t.Fatalf("bad filename length=%d value=%q", len(got), got)
	}
}
