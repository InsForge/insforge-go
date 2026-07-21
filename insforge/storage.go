package insforge

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Storage provides file storage operations.
type Storage struct {
	http *httpClient
}

func newStorage(h *httpClient) *Storage { return &Storage{http: h} }

// From returns a StorageBucket for the given bucket name.
func (s *Storage) From(bucketName string) *StorageBucket {
	return &StorageBucket{http: s.http, bucket: bucketName}
}

// StorageBucket wraps operations for a specific bucket.
type StorageBucket struct {
	http   *httpClient
	bucket string
}

func (b *StorageBucket) base() string {
	return "/api/storage/buckets/" + b.bucket
}

// GetPublicURL returns the public URL for an object in the bucket.
func (b *StorageBucket) GetPublicURL(path string) string {
	clean := strings.TrimLeft(path, "/")
	return fmt.Sprintf("%s%s/objects/%s", b.http.baseURL, b.base(), clean)
}

// generateObjectKey derives a unique object key from a filename:
// "<sanitized-base>-<timestamp>-<random><ext>". Auto-key generation is a
// client-side convenience — the storage API has no server-side key minting —
// so UploadAuto produces the key here and then uploads through the standard
// Upload path.
func generateObjectKey(filename string) string {
	base, ext := filename, ""
	if dot := strings.LastIndex(filename, "."); dot > 0 {
		base, ext = filename[:dot], filename[dot:]
	}
	sanitized := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		default:
			return '-'
		}
	}, base)
	if len(sanitized) > 32 {
		sanitized = sanitized[:32]
	}
	if sanitized == "" {
		sanitized = "file"
	}
	return fmt.Sprintf("%s-%d-%s%s", sanitized, time.Now().UnixMilli(), randomKeySuffix(), ext)
}

// randomKeySuffix returns 6 random base36 characters for collision avoidance.
func randomKeySuffix() string {
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	buf := make([]byte, 6)
	if _, err := rand.Read(buf); err != nil {
		// crypto/rand should never fail; fall back to a timestamp-derived suffix.
		return strconv.FormatInt(time.Now().UnixNano()%2176782336, 36)
	}
	for i := range buf {
		buf[i] = alphabet[int(buf[i])%len(alphabet)]
	}
	return string(buf)
}

// Upload uploads raw bytes to the given path in the bucket.
// Standard PUT semantics: uploading to an existing key replaces the current
// object in place (requires an InsForge backend with standard-PUT storage).
func (b *StorageBucket) Upload(ctx context.Context, path string, data []byte, contentType string) Result[interface{}] {
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	clean := strings.TrimLeft(path, "/")

	// Request upload strategy
	stratRaw, err := b.http.post(ctx, b.base()+"/upload-strategy", map[string]interface{}{
		"filename": clean, "contentType": contentType, "size": len(data),
	}, nil)
	if err != nil {
		return fail[interface{}](err)
	}

	strategy, _ := stratRaw.(map[string]interface{})
	method, _ := strategy["method"].(string)
	uploadURL, _ := strategy["uploadUrl"].(string)
	key, _ := strategy["key"].(string)
	if key == "" {
		key = clean
	}

	if method == "presigned" && uploadURL != "" {
		// S3 presigned POST: multipart/form-data with strategy fields + file content
		var body bytes.Buffer
		mw := multipart.NewWriter(&body)
		// Write all signed fields first (policy, signature, key, etc.)
		if fields, ok := strategy["fields"].(map[string]interface{}); ok {
			for k, v := range fields {
				_ = mw.WriteField(k, fmt.Sprintf("%v", v))
			}
		}
		// File content must be last
		fw, err := mw.CreateFormFile("file", key)
		if err != nil {
			return fail[interface{}](err)
		}
		if _, err = fw.Write(data); err != nil {
			return fail[interface{}](err)
		}
		mw.Close()

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, uploadURL, &body)
		if err != nil {
			return fail[interface{}](err)
		}
		req.Header.Set("Content-Type", mw.FormDataContentType())
		resp, err := b.http.client.Do(req)
		if err != nil {
			return fail[interface{}](err)
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 400 {
			return fail[interface{}](&InsForgeError{StatusCode: resp.StatusCode, Message: "presigned upload failed"})
		}
		// Confirm if required
		confirmReq, _ := strategy["confirmRequired"].(bool)
		if confirmReq {
			confirmURL, _ := strategy["confirmUrl"].(string)
			// confirmUrl may be a relative path or full URL; strip base URL if present
			confirmPath := strings.Replace(confirmURL, b.http.baseURL, "", 1)
			raw, err := b.http.post(ctx, confirmPath, map[string]interface{}{"size": len(data), "contentType": contentType}, nil)
			if err != nil {
				return fail[interface{}](err)
			}
			return ok[interface{}](raw)
		}
		return ok[interface{}](map[string]interface{}{"key": key})
	}

	// Direct upload: use the uploadUrl from strategy (has the URL-encoded key)
	directPath := strings.Replace(uploadURL, b.http.baseURL, "", 1)
	if directPath == "" {
		directPath = b.base() + "/objects/" + key
	}
	raw, err := b.http.uploadRaw(ctx, directPath, data, contentType)
	if err != nil {
		return fail[interface{}](err)
	}
	return ok[interface{}](raw)
}

// UploadReader uploads from an io.Reader.
// Standard PUT semantics: uploading to an existing key replaces the current
// object in place (requires an InsForge backend with standard-PUT storage).
func (b *StorageBucket) UploadReader(ctx context.Context, path string, r io.Reader, contentType string) Result[interface{}] {
	data, err := io.ReadAll(r)
	if err != nil {
		return fail[interface{}](&InsForgeError{Message: "failed to read upload data: " + err.Error()})
	}
	return b.Upload(ctx, path, data, contentType)
}

// UploadAuto uploads raw bytes under an automatically generated,
// collision-free key. The key is derived client-side from filename
// (sanitized base + timestamp + random suffix) and uploaded through the
// standard Upload path, so repeated uploads of the same file never overwrite
// each other. Pass an empty filename to use the default "file" base.
func (b *StorageBucket) UploadAuto(ctx context.Context, filename string, data []byte, contentType string) Result[interface{}] {
	return b.Upload(ctx, generateObjectKey(filename), data, contentType)
}

// UploadAutoReader uploads from an io.Reader under an automatically
// generated, collision-free key. See UploadAuto.
func (b *StorageBucket) UploadAutoReader(ctx context.Context, filename string, r io.Reader, contentType string) Result[interface{}] {
	data, err := io.ReadAll(r)
	if err != nil {
		return fail[interface{}](&InsForgeError{Message: "failed to read upload data: " + err.Error()})
	}
	return b.Upload(ctx, generateObjectKey(filename), data, contentType)
}

// Download downloads an object and returns its raw bytes.
func (b *StorageBucket) Download(ctx context.Context, path string) Result[[]byte] {
	clean := strings.TrimLeft(path, "/")

	// Get download strategy
	stratRaw, err := b.http.post(ctx, b.base()+"/objects/"+clean+"/download-strategy",
		map[string]interface{}{"expiresIn": 3600}, nil)
	if err != nil {
		// Fallback: direct download
		data, derr := b.http.downloadBytes(ctx, b.base()+"/objects/"+clean)
		if derr != nil {
			return fail[[]byte](err)
		}
		return ok(data)
	}

	strategy, _ := stratRaw.(map[string]interface{})
	method, _ := strategy["method"].(string)
	downloadURL, _ := strategy["url"].(string)

	if downloadURL == "" {
		data, err := b.http.downloadBytes(ctx, b.base()+"/objects/"+clean)
		if err != nil {
			return fail[[]byte](err)
		}
		return ok(data)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return fail[[]byte](err)
	}
	// For direct downloads, include auth headers
	if method == "direct" {
		if token := b.http.authToken(); token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		for k, v := range b.http.headers {
			req.Header.Set(k, v)
		}
	}
	resp, err := b.http.client.Do(req)
	if err != nil {
		return fail[[]byte](err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fail[[]byte](&InsForgeError{StatusCode: resp.StatusCode, Message: "download failed"})
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return fail[[]byte](err)
	}
	return ok(data)
}

// List lists objects in the bucket.
func (b *StorageBucket) List(ctx context.Context, opts *ListOptions) Result[interface{}] {
	params := url.Values{}
	params.Set("limit", "100")
	params.Set("offset", "0")
	if opts != nil {
		if opts.Prefix != "" {
			params.Set("prefix", opts.Prefix)
		}
		if opts.Search != "" {
			params.Set("search", opts.Search)
		}
		if opts.Limit > 0 {
			params.Set("limit", strconv.Itoa(opts.Limit))
		}
		if opts.Offset > 0 {
			params.Set("offset", strconv.Itoa(opts.Offset))
		}
	}
	raw, err := b.http.get(ctx, b.base()+"/objects", params, nil)
	if err != nil {
		return fail[interface{}](err)
	}
	return ok[interface{}](raw)
}

// Remove deletes an object from the bucket.
func (b *StorageBucket) Remove(ctx context.Context, path string) Result[interface{}] {
	clean := strings.TrimLeft(path, "/")
	raw, err := b.http.delete(ctx, b.base()+"/objects/"+clean, nil, nil)
	if err != nil {
		return fail[interface{}](err)
	}
	return ok[interface{}](raw)
}

// ListOptions controls list pagination and filtering.
type ListOptions struct {
	Prefix string
	Search string
	Limit  int
	Offset int
}
