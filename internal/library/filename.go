package library

import (
	"path"
	"strings"
	"unicode"

	"github.com/xtawa/tunebridge/internal/model"
)

const maxFilenameBytes = 240

// AudioFilename returns only a presentation name. Callers must retain the
// TrackIdentity separately for routing and database lookups.
func AudioFilename(track model.Track, extension string) string {
	base := SanitizeFilename(track.Title)
	artist := SanitizeFilename(strings.Join(track.Artists, ", "))
	if artist != "" {
		base += " - " + artist
	}
	return withExtension(base, extension)
}

func LyricsFilename(audioFilename string) string {
	return strings.TrimSuffix(audioFilename, path.Ext(audioFilename)) + ".lrc"
}

func CoverFilename(audioFilename string) string {
	return strings.TrimSuffix(audioFilename, path.Ext(audioFilename)) + ".cover"
}

func CollisionFilename(audioFilename string, identity model.TrackIdentity) string {
	ext := path.Ext(audioFilename)
	base := strings.TrimSuffix(audioFilename, ext)
	suffix := " [" + SanitizeFilename(identity.SourceID+"-"+identity.TrackID) + "]"
	return truncateFilename(base, suffix, ext)
}

func SanitizeFilename(value string) string {
	value = strings.Map(func(r rune) rune {
		switch {
		case r < 32 || r == 127:
			return -1
		case strings.ContainsRune(`/\\:?*"<>|`, r):
			return ' '
		default:
			return r
		}
	}, value)
	value = strings.Join(strings.FieldsFunc(value, unicode.IsSpace), " ")
	value = strings.Trim(value, ". ")
	if value == "" {
		return "untitled"
	}
	return truncateFilename(value, "", "")
}

func withExtension(base, extension string) string {
	extension = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(extension)), ".")
	if extension == "" {
		return truncateFilename(base, "", "")
	}
	return truncateFilename(base, "", "."+extension)
}

func truncateFilename(base, suffix, extension string) string {
	budget := maxFilenameBytes - len(suffix) - len(extension)
	if budget < 1 {
		budget = 1
	}
	var builder strings.Builder
	for _, r := range base {
		if builder.Len()+len(string(r)) > budget {
			break
		}
		builder.WriteRune(r)
	}
	if builder.Len() == 0 {
		builder.WriteString("untitled")
	}
	return builder.String() + suffix + extension
}
