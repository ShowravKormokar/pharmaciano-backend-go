// Package storage is the abstract file-object channel. It exists so the rest of the app never
// touches os.* directly: repositories/services call Store.Put/Get/Delete and
// stay ignorant of where bytes physically live, exactly like the Mailer
// interface abstracts email delivery.

package storage

import (
	"backend/internal/platform/config"
	"backend/internal/platform/telemetry"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"
)

// Store is a minimal interface every driver satisfies. Today we ship a local
// disk driver; future work adds S3 without changing callers.
type Store interface {
	Put(ctx context.Context, obj ObjectInput) (ObjectRef, error)
	Get(ctx context.Context, ref ObjectRef) (io.ReadCloser, ObjectInfo, error)
	Delete(ctx context.Context, ref ObjectRef) error
	URL(ctx context.Context, ref ObjectRef, ttl time.Duration) (string, error)
	Close() error
}

// ObjectInput is what the caller uploads.
type ObjectInput struct {
	Namespace    string
	OriginalName string
	ContentType  string
	Reader       io.Reader
	SizeHint     int64
	Public       bool
}

// ObjectRef identifies an object after Put. Store it in DB; opaque to
// callers — always obtained from Put, never hand-constructed.
type ObjectRef struct {
	Namespace string
	Key       string
}

// ObjectInfo carries metadata returned by Get.
type ObjectInfo struct {
	ContentType string
	Size        int64
	SHA256      string
	CreatedAt   time.Time
}

// Errors.
var (
	ErrNotFound         = errors.New("storage: object not found")
	ErrDriverNotEnabled = errors.New("storage: driver not enabled")
	ErrTooLarge         = errors.New("storage: object exceeds max size")
	ErrMimeNotAllowed   = errors.New("storage: mime type not allowed")
	// ErrInvalidInput signals a programming error at the call site (nil reader, malformed namespace) — never something an end user triggers.
	ErrInvalidInput = errors.New("storage: invalid input")
	// ErrInvalidRef signals a caller-supplied ObjectRef that cannot be resolved to a safe on-disk path.
	ErrInvalidRef = errors.New("storage: invalid object reference")
)

// New builds the driver named in cfg.Driver. m may be nil (metrics optional);
func New(cfg config.StorageConfig, log *zap.Logger, m *telemetry.Metrics) (store, error) {
	if log == nil {
		log = zap.NewNop()
	}
	switch strings.ToLower(strings.TrimSpace(cfg.Driver)) {
	case "", "local":
		return newLocal(cfg, log, m)
	case "s3":
		// Concrete S3 driver ships in a later commit — placeholder for now.
		return nil, fmt.Errorf("%w: s3 driver not implemented yet", ErrDriverNotEnabled)
	default:
		return nil, fmt.Errorf("storage: unknown driver %q", cfg.Driver)
	}
}

// Local disk driver ******************

// local implements Store against the filesystem. All fields are set once at
// construction and never mutated afterward, so *local is safe for
// concurrent use across goroutines without a mutex.
type local struct {
	root         string
	maxSize      int64
	allowedMimes map[string]struct{}
	fsync        bool
	log          *zap.Logger
	metrics      *telemetry.Metrics
}

const metaSuffix = ".meta.json"

type objectMeta struct {
	Namespace           string    `json:"namespace"`
	OriginalName        string    `json:"original_name"`
	ContentType         string    `json:"content_type"`          // sniffed / effective type
	DeclaredContentType string    `json:"declared_content_type"` // what the caller claimed
	SHA256              string    `json:"sha256"`
	Size                int64     `json:"size"`
	CreatedAt           time.Time `json:"created_at"`
}

var namespaceRe = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

func newLocal(cfg config.StorageConfig, log *zap.Logger, m *telemetry.Metrics) (*local, error) {
	if cfg.LocalPath == "" {
		return nil, errors.New("storage.local_path is required")
	}

	if cfg.MaxFileSizeMB <= 0 {
		return nil, errors.New("storage.max_file_size_mb must be > 0")
	}
	if err := os.MkdirAll(cfg.LocalPath, 0o750); err != nil {
		return nil, fmt.Errorf("mkdir %s: %w", cfg.LocalPath, err)
	}
	mimes := map[string]struct{}{}
	for _, mt := range cfg.AllowedMimeTypes {
		mimes[baseMime(mt)] = struct{}{}
	}

	if len(mimes) == 0 {
		log.Warn("storage: allowed_mime_types is empty — ALL content types will be accepted; " +
			"this is unsafe outside local dev")
	}

	return &local{
		root:         cfg.LocalPath,
		maxSize:      int64(cfg.MaxFileSizeMB) * 1024 * 1024,
		allowedMimes: mimes,
		fsync:        cfg.FsyncOnWrite,
		log:          log,
		metrics:      m,
	}, nil
}

// ----------- PUT --------------
func (l *local) Put(ctx context.Context, obj ObjectInput) (ObjectRef, error) {
	ctx, span := telemetry.StartSpan(
		ctx, "storage.local.Put", attribute.String("storage.provider", "local"),
	)

	start := time.Now()
	ref, n, err := l.putObject(ctx, obj)
	telemetry.EndSpan(span, err)

	if l.metrics != nil {
		l.metrics.ObserveStorageOp("local", "put", time.Since(start), err)
		if err == nil {
			l.metrics.AddStorageBytes("local", "put", n)
		}
	}
	return ref, err
}

func (l *local) putObject(ctx context.Context, obj ObjectInput) (ObjectRef, int64, error) {
	if err := ctxErr(ctx); err != nil {
		return ObjectRef{}, 0, err
	}
	if obj.Reader == nil {
		return ObjectRef{}, 0, fmt.Errorf("%w: reader is nil", ErrInvalidInput)
	}
	ns, err := validateNamespace(obj.Namespace)
	if err != nil {
		return ObjectRef{}, 0, err
	}
	if obj.SizeHint > 0 && obj.SizeHint > l.maxSize {
		return ObjectRef{}, 0, ErrTooLarge
	}

	peeked, detected, err := sniffContentType(obj.Reader)
	if err != nil {
		return ObjectRef{}, 0, fmt.Errorf("storage: sniff content type: %w", err)
	}

	if obj.ContentType != "" && baseMime(obj.ContentType) != baseMime(detected) {
		l.log.Warn("storage: declared content-type does not match sniffed content-type",
			zap.String("declared", obj.ContentType),
			zap.String("detected", detected),
			zap.String("namespace", ns),
		)
	}

	if err := l.checkMime(detected); err != nil {
		return ObjectRef{}, 0, err
	}

	now := time.Now().UTC()
	name := sanitizeName(obj.OriginalName)
	key := fmt.Sprintf("%s/%d/%02d/%s-%s", ns, now.Year(), now.Month(), uuid.NewString(), name)

	fullPath, err := l.resolvePath(key)
	if err != nil {
		return ObjectRef{}, 0, err
	}
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o750); err != nil {
		return ObjectRef{}, 0, fmt.Errorf("storage: mkdir: %w", err)
	}
}

// ---- URL / Close -----------------------------------
// URL returns an app-served path; the caller's own HTTP handler must enforce
// authorization before actually reading the file. Segments are percent-
// encoded so a namespace/key can never break out of the URL path structure.
func (l *local) URL(ctx context.Context, ref ObjectRef, _ time.Duration) (string, error) {
	if err := ctxErr(ctx); err != nil {
		return "", err
	}
	parts := strings.Split(ref.Key, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return fmt.Sprintf("/api/v1/files/%s/%s", url.PathEscape(ref.Namespace), strings.Join(parts, "/")), nil
}

func (l *local) Close() error {
	return nil
}

// ---- internals ------------------------
func (l *local) resolvePath(key string) (string, error) {
	if key == "" {
		return "", fmt.Errorf("%w: empty key", ErrInvalidRef)
	}
	full := filepath.Clean(filepath.Join(l.root, filepath.FromSlash(key)))
	rootClean := filepath.Clean(l.root)
	rel, err := filepath.Rel(rootClean, full)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%w: resolved path escapes storage root", ErrInvalidRef)
	}
	return full, nil
}

func (l *local) checkMime(mime string) error {
	if len(l.allowedMimes) == 0 {
		return nil // permissive mode — warned about once at construction
	}
	if _, ok := l.allowedMimes[baseMime(mime)]; !ok {
		return fmt.Errorf("%w: %s", ErrMimeNotAllowed, mime)
	}
	return nil
}

func (l *local) writeMeta(fullPath string, m objectMeta) error {
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return os.WriteFile(fullPath+metaSuffix, data, 0o640)
}

func (l *local) readMeta(fullPath string) (objectMeta, error) {
	var m objectMeta
	data, err := os.ReadFile(fullPath + metaSuffix)
	if err != nil {
		return m, err
	}
	err = json.Unmarshal(data, &m)
	return m, err
}

func validateNamespace(ns string) (string, error) {
	ns = strings.TrimSpace(ns)
	if !namespaceRe.MatchString(ns) {
		return "", fmt.Errorf("%w: namespace %q must match %s", ErrInvalidInput, ns, namespaceRe.String())
	}
	return ns, nil
}

// sanitizeName strips path components, hostile characters, and Windows-
// reserved device names from a user-supplied filename.
func sanitizeName(s string) string {
	s = filepath.Base(strings.TrimSpace(s))
	if s == "" || s == "." || s == "/" {
		return "file"
	}

	var out strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '.', r == '-', r == '_':
			out.WriteRune(r)
		default:
			out.WriteRune('_')
		}
	}

	name := out.String()

	base := strings.TrimSuffix(name, filepath.Ext(name))
	if _, reserved := windowsReservedNames[strings.ToUpper(base)]; reserved {
		name = "_" + name
	}

	if len(name) > 128 {
		name = name[:128]
	}
	return name
}

var windowsReservedNames = map[string]struct{}{
	"CON": {}, "PRN": {}, "AUX": {}, "NUL": {},
	"COM1": {}, "COM2": {}, "COM3": {}, "COM4": {}, "COM5": {},
	"COM6": {}, "COM7": {}, "COM8": {}, "COM9": {},
	"LPT1": {}, "LPT2": {}, "LPT3": {}, "LPT4": {}, "LPT5": {},
	"LPT6": {}, "LPT7": {}, "LPT8": {}, "LPT9": {},
}

// baseMime strips any ";charset=..." parameter so comparisons and allowlist
// lookups aren't fooled by "text/plain; charset=utf-8" vs "text/plain".
func baseMime(mime string) string {
	if i := strings.IndexByte(mime, ';'); i >= 0 {
		mime = mime[:i]
	}
	return strings.ToLower(strings.TrimSpace(mime))
}

func sniffContentType(r io.Reader) (io.Reader, string, error) {
	buf := make([]byte, 512)
	n, err := io.ReadFull(r, buf)
	switch {
	case err == nil, errors.Is(err, io.ErrUnexpectedEOF), errors.Is(err, io.EOF):
		// n bytes were read (possibly < 512, including 0 for an empty file).
	default:
		return nil, "", err
	}
	buf = buf[:n]
	return io.MultiReader(bytes.NewReader(buf), r), http.DetectContentType(buf), nil
}

func sniffFromFile(f *os.File) (string, error) {
	buf := make([]byte, 512)
	n, err := f.Read(buf)
	if err != nil && err != io.EOF {
		return "", err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	return http.DetectContentType(buf[:n]), nil
}

type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c *ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}

func ctxErr(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}

func removeWithRetry(path string, attempts int, delay time.Duration) error {
	var err error
	for i := 0; i < attempts; i++ {
		err = os.Remove(path)
		if err == nil || errors.Is(err, os.ErrNotExist) {
			return err
		}
		time.Sleep(delay)
	}
	return err
}

type countingReadCloser struct {
	io.ReadCloser
	n       int64
	onClose func(bytes int64, err error)
}

func (c *countingReadCloser) Read(p []byte) (int, error) {
	n, err := c.ReadCloser.Read(p)
	c.n += int64(n)
	return n, err
}

func (c *countingReadCloser) Close() error {
	err := c.ReadCloser.Close()
	if c.onClose != nil {
		c.onClose(c.n, err)
	}
	return err
}

// HashHex is a helper for tests and audits.
func HashHex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
