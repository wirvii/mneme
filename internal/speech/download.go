package speech

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// HTTPFetcher downloads pinned catalog artifacts with bounded redirects and no credentials.
func HTTPFetcher(client *http.Client) FetchArtifact {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Minute}
	}
	clone := *client
	clone.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("speech: too many artifact redirects")
		}
		if req.URL.Scheme != "https" || req.URL.User != nil {
			return errors.New("speech: unsafe artifact redirect")
		}
		return nil
	}
	return func(ctx context.Context, artifact Artifact, dst io.Writer) error {
		parsed, err := url.Parse(artifact.URL)
		if err != nil || parsed.Scheme != "https" || parsed.User != nil {
			return errors.New("speech: unsafe artifact URL")
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, artifact.URL, nil)
		if err != nil {
			return fmt.Errorf("speech: create artifact request: %w", err)
		}
		response, err := clone.Do(req)
		if err != nil {
			return fmt.Errorf("speech: download artifact: %w", err)
		}
		defer func() { _ = response.Body.Close() }()
		if response.StatusCode != http.StatusOK {
			return fmt.Errorf("speech: artifact server returned HTTP %d", response.StatusCode)
		}
		if response.ContentLength >= 0 && response.ContentLength != artifact.Size {
			return errors.New("speech: artifact content length mismatch")
		}
		if _, err := io.Copy(dst, response.Body); err != nil {
			return fmt.Errorf("speech: read artifact response: %w", err)
		}
		return nil
	}
}
