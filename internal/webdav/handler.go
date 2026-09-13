package webdav

import (
	"encoding/xml"
	"errors"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/xtawa/tunebridge/internal/auth"
)

type Handler struct {
	library  Library
	username string
	password string
}

func NewHandler(library Library, username, password string) *Handler {
	return &Handler{library: library, username: username, password: password}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !h.authorized(w, r) {
		return
	}
	switch r.Method {
	case http.MethodOptions:
		h.options(w)
	case "PROPFIND":
		h.propfind(w, r)
	case http.MethodHead:
		h.head(w, r)
	case http.MethodGet:
		h.get(w, r)
	default:
		w.Header().Set("Allow", allowHeader)
		http.Error(w, "read-only WebDAV method not allowed", http.StatusMethodNotAllowed)
	}
}

const allowHeader = "OPTIONS, PROPFIND, GET, HEAD"

func (h *Handler) authorized(w http.ResponseWriter, r *http.Request) bool {
	if auth.ValidBasic(r, h.username, h.password) {
		return true
	}
	w.Header().Set("WWW-Authenticate", `Basic realm="TuneBridge WebDAV", charset="UTF-8"`)
	http.Error(w, "WebDAV authentication required", http.StatusUnauthorized)
	return false
}

func (h *Handler) options(w http.ResponseWriter) {
	w.Header().Set("DAV", "1")
	w.Header().Set("Allow", allowHeader)
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) propfind(w http.ResponseWriter, r *http.Request) {
	resourcePath, ok := requestResourcePath(r.URL)
	if !ok {
		http.Error(w, "invalid WebDAV path", http.StatusBadRequest)
		return
	}
	resource, err := h.library.Stat(r.Context(), resourcePath)
	if errors.Is(err, ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "read virtual resource", http.StatusInternalServerError)
		return
	}
	depth, err := parseDepth(r.Header.Get("Depth"))
	if err != nil {
		http.Error(w, "only Depth: 0 or Depth: 1 is supported", http.StatusForbidden)
		return
	}
	resources := []Resource{resource}
	if depth == 1 && resource.IsCollection() {
		children, err := h.library.List(r.Context(), resourcePath)
		if errors.Is(err, ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		if err != nil {
			http.Error(w, "list virtual resource", http.StatusInternalServerError)
			return
		}
		resources = append(resources, children...)
	}

	response := multiStatus{XMLNS: "DAV:", Responses: make([]davResponse, 0, len(resources))}
	for _, item := range resources {
		response.Responses = append(response.Responses, davResponseFrom(item))
	}
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.WriteHeader(http.StatusMultiStatus)
	_, _ = w.Write([]byte(xml.Header))
	_ = xml.NewEncoder(w).Encode(response)
}

func (h *Handler) head(w http.ResponseWriter, r *http.Request) {
	resourcePath, ok := requestResourcePath(r.URL)
	if !ok {
		http.Error(w, "invalid WebDAV path", http.StatusBadRequest)
		return
	}
	resource, err := h.library.Stat(r.Context(), resourcePath)
	if errors.Is(err, ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "read virtual resource", http.StatusInternalServerError)
		return
	}
	if content, ok := h.library.(ContentLibrary); ok && !resource.IsCollection() {
		resource, err = content.Head(r.Context(), resource)
		if err != nil {
			http.Error(w, "describe virtual resource", http.StatusBadGateway)
			return
		}
	}
	setResourceHeaders(w, resource)
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	resourcePath, ok := requestResourcePath(r.URL)
	if !ok {
		http.Error(w, "invalid WebDAV path", http.StatusBadRequest)
		return
	}
	resource, err := h.library.Stat(r.Context(), resourcePath)
	if errors.Is(err, ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "read virtual resource", http.StatusInternalServerError)
		return
	}
	if resource.IsCollection() {
		http.Error(w, "cannot GET a WebDAV collection", http.StatusMethodNotAllowed)
		return
	}
	content, ok := h.library.(ContentLibrary)
	if !ok {
		http.Error(w, "stream resolver is not configured", http.StatusServiceUnavailable)
		return
	}
	if err := content.Get(r.Context(), w, r, resource); err != nil {
		// A streaming response may already be committed. Do not append an error
		// body that corrupts audio; the request log retains the failure context.
		return
	}
}

func requestResourcePath(location *url.URL) (string, bool) {
	decoded, err := url.PathUnescape(location.EscapedPath())
	if err != nil || !utf8.ValidString(decoded) || strings.Contains(decoded, "\\") {
		return "", false
	}
	if decoded == "" {
		decoded = "/"
	}
	if !strings.HasPrefix(decoded, "/") {
		return "", false
	}
	for _, part := range strings.Split(decoded, "/") {
		if part == "." || part == ".." {
			return "", false
		}
	}
	return path.Clean(decoded), true
}

func parseDepth(raw string) (int, error) {
	switch strings.TrimSpace(raw) {
	case "", "0":
		return 0, nil
	case "1":
		return 1, nil
	default:
		return 0, errors.New("unsupported depth")
	}
}

func setResourceHeaders(w http.ResponseWriter, resource Resource) {
	if resource.ContentType != "" {
		w.Header().Set("Content-Type", resource.ContentType)
	}
	if resource.Size >= 0 && !resource.IsCollection() {
		w.Header().Set("Accept-Ranges", "bytes")
		w.Header().Set("Content-Length", strconv.FormatInt(resource.Size, 10))
	}
	if !resource.ModifiedAt.IsZero() {
		w.Header().Set("Last-Modified", resource.ModifiedAt.UTC().Format(http.TimeFormat))
	}
	if resource.ETag != "" {
		w.Header().Set("ETag", resource.ETag)
	}
}

type multiStatus struct {
	XMLName   xml.Name      `xml:"multistatus"`
	XMLNS     string        `xml:"xmlns,attr"`
	Responses []davResponse `xml:"response"`
}

type davResponse struct {
	Href     string   `xml:"href"`
	Propstat propstat `xml:"propstat"`
}

type propstat struct {
	Prop   davProperty `xml:"prop"`
	Status string      `xml:"status"`
}

type davProperty struct {
	ResourceType     resourceType `xml:"resourcetype"`
	DisplayName      string       `xml:"displayname"`
	GetContentLength string       `xml:"getcontentlength,omitempty"`
	GetContentType   string       `xml:"getcontenttype,omitempty"`
	GetLastModified  string       `xml:"getlastmodified,omitempty"`
}

type resourceType struct {
	Collection *struct{} `xml:"collection,omitempty"`
}

func davResponseFrom(resource Resource) davResponse {
	resourceType := resourceType{}
	if resource.IsCollection() {
		resourceType.Collection = &struct{}{}
	}
	href := escapedHref(resource.Path)
	if resource.IsCollection() && !strings.HasSuffix(href, "/") {
		href += "/"
	}
	property := davProperty{ResourceType: resourceType, DisplayName: path.Base(resource.Path)}
	if resource.Path == "/" {
		property.DisplayName = "TuneBridge"
	}
	if !resource.IsCollection() && resource.Size >= 0 {
		property.GetContentLength = strconv.FormatInt(resource.Size, 10)
	}
	if resource.ContentType != "" {
		property.GetContentType = resource.ContentType
	}
	if !resource.ModifiedAt.IsZero() {
		property.GetLastModified = resource.ModifiedAt.UTC().Format(http.TimeFormat)
	}
	return davResponse{Href: href, Propstat: propstat{Prop: property, Status: "HTTP/1.1 200 OK"}}
}

func escapedHref(resourcePath string) string {
	parts := strings.Split(strings.TrimPrefix(resourcePath, "/"), "/")
	if resourcePath == "/" {
		return "/"
	}
	for index, part := range parts {
		parts[index] = url.PathEscape(part)
	}
	return "/" + strings.Join(parts, "/")
}
