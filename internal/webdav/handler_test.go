package webdav

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOptionsRequiresAuthenticationAndAdvertisesReadOnlyMethods(t *testing.T) {
	t.Parallel()
	handler := NewHandler(BootstrapLibrary(), "player", "password")

	unauthorized := httptest.NewRecorder()
	handler.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodOptions, "/", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d", unauthorized.Code)
	}

	request := httptest.NewRequest(http.MethodOptions, "/", nil)
	request.SetBasicAuth("player", "password")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent || response.Header().Get("DAV") != "1" {
		t.Fatalf("unexpected OPTIONS response: %d %#v", response.Code, response.Header())
	}
}

func TestPropfindDepthOneEscapesUnicodeAndXML(t *testing.T) {
	t.Parallel()
	library, err := NewMemoryLibrary(
		Resource{Path: "/", Kind: Collection},
		Resource{Path: "/网易云 & <收藏>", Kind: Collection},
	)
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(library, "player", "password")
	request := httptest.NewRequest("PROPFIND", "/", nil)
	request.Header.Set("Depth", "1")
	request.SetBasicAuth("player", "password")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusMultiStatus {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	if !strings.Contains(body, "%E7%BD%91%E6%98%93%E4%BA%91%20&amp;%20%3C%E6%94%B6%E8%97%8F%3E") || !strings.Contains(body, "网易云 &amp; &lt;收藏&gt;") {
		t.Fatalf("unexpected PROPFIND XML: %s", body)
	}
}

func TestPropfindRejectsUnsupportedDepth(t *testing.T) {
	t.Parallel()
	handler := NewHandler(BootstrapLibrary(), "player", "password")
	request := httptest.NewRequest("PROPFIND", "/", nil)
	request.Header.Set("Depth", "2")
	request.SetBasicAuth("player", "password")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d", response.Code)
	}
}

func TestPropfindDepthInfinityRecursesCollections(t *testing.T) {
	t.Parallel()
	library, err := NewMemoryLibrary(
		Resource{Path: "/", Kind: Collection},
		Resource{Path: "/网易云", Kind: Collection},
		Resource{Path: "/网易云/我喜欢的音乐", Kind: Collection},
		Resource{Path: "/网易云/我喜欢的音乐/晴天.mp3", Kind: AudioFile, Size: 1024},
	)
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(library, "player", "password")
	request := httptest.NewRequest("PROPFIND", "/网易云", nil)
	request.Header.Set("Depth", "infinity")
	request.SetBasicAuth("player", "password")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusMultiStatus {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	if !strings.Contains(body, "晴天.mp3") || !strings.Contains(body, "我喜欢的音乐") {
		t.Fatalf("PROPFIND infinity did not return recursive children: %s", body)
	}
	if !strings.Contains(body, "<getetag>") || !strings.Contains(body, "</getetag>") {
		t.Fatalf("PROPFIND response missing <getetag>: %s", body)
	}
	if !strings.Contains(body, "<getlastmodified>") || !strings.Contains(body, "</getlastmodified>") {
		t.Fatalf("PROPFIND response missing <getlastmodified>: %s", body)
	}
}
