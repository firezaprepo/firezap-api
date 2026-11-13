package httpapi

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/firezaprepo/firezap-api/internal/instance"
	"github.com/firezaprepo/firezap-api/internal/webhook"
)

// errFetch is a media URL that could not be read; the message says why.
type errFetch struct{ msg string }

func (e errFetch) Error() string { return e.msg }

func (s *Server) sendMedia(w http.ResponseWriter, r *http.Request) {
	var body struct {
		To       string     `json:"to"`
		Type     string     `json:"type"`
		URL      string     `json:"url"`
		Base64   string     `json:"base64"`
		Mimetype string     `json:"mimetype"`
		FileName string     `json:"fileName"`
		Caption  string     `json:"caption"`
		PTT      bool       `json:"ptt"`
		ReplyTo  *replyBody `json:"replyTo"`
	}
	// base64 makes the file a third bigger; leave room for the other fields too.
	if !decodeLimit(w, r, &body, s.mediaMax*4/3+64<<10) {
		return
	}
	body.URL, body.Base64 = strings.TrimSpace(body.URL), strings.TrimSpace(body.Base64)
	if (body.URL == "") == (body.Base64 == "") {
		writeError(w, http.StatusBadRequest, "send the file in exactly one of url or base64")
		return
	}
	if utf8.RuneCountInString(body.Caption) > maxTextLength {
		writeError(w, http.StatusBadRequest, "caption is too long (max 4096 characters)")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()

	var (
		data     []byte
		detected string // mimetype from the data URL prefix or the HTTP response
	)
	if body.Base64 != "" {
		raw := body.Base64
		if strings.HasPrefix(raw, "data:") { // data:image/png;base64,....
			head, rest, ok := strings.Cut(raw, ",")
			if !ok || !strings.HasSuffix(head, ";base64") {
				writeError(w, http.StatusBadRequest, "base64 data URL must look like data:<mimetype>;base64,<data>")
				return
			}
			detected, raw = strings.TrimSuffix(strings.TrimPrefix(head, "data:"), ";base64"), rest
		}
		var err error
		if data, err = base64.StdEncoding.DecodeString(raw); err != nil {
			writeError(w, http.StatusBadRequest, "base64 is not valid")
			return
		}
		if int64(len(data)) > s.mediaMax {
			s.fail(w, instance.ErrMediaTooLarge)
			return
		}
	} else {
		var err error
		data, detected, err = s.fetchMedia(ctx, body.URL)
		var fe errFetch
		switch {
		case errors.As(err, &fe):
			writeError(w, http.StatusBadRequest, fe.msg)
			return
		case err != nil:
			s.fail(w, err)
			return
		}
		if body.FileName == "" {
			if u, err := url.Parse(body.URL); err == nil {
				if base := path.Base(u.Path); base != "/" && base != "." {
					body.FileName = base
				}
			}
		}
	}

	mt := strings.TrimSpace(body.Mimetype)
	if mt == "" {
		mt = detected
	}
	if base, _, err := mime.ParseMediaType(mt); err != nil || base == "application/octet-stream" || base == "binary/octet-stream" {
		mt = http.DetectContentType(data) // generic or missing: look at the bytes
	}

	id, err := s.manager.SendMedia(ctx, r.PathValue("id"), body.To, instance.OutgoingMedia{
		Kind: strings.ToLower(strings.TrimSpace(body.Type)), Data: data, Mimetype: mt,
		FileName: strings.TrimSpace(body.FileName), Caption: body.Caption, PTT: body.PTT,
		Quote: body.ReplyTo.quote(),
	})
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"id": id})
}

// fetchMedia downloads a file to send. The URL comes from the API user, so it follows the
// same address rules as webhooks: checked now and again on the IP actually dialed.
func (s *Server) fetchMedia(ctx context.Context, raw string) ([]byte, string, error) {
	if err := webhook.ValidateURL(ctx, raw, s.policy, net.DefaultResolver.LookupNetIP); err != nil {
		return nil, "", errFetch{"media url: " + err.Error()}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, "", errFetch{"media url is not valid"}
	}
	req.Header.Set("User-Agent", "FireZAP-API/"+Version)
	resp, err := s.fetcher.Do(req)
	if err != nil {
		if errors.Is(err, webhook.ErrBlockedAddress) {
			return nil, "", errFetch{"media url: " + webhook.ErrBlockedAddress.Error()}
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, "", err
		}
		return nil, "", errFetch{"could not download the media url"}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", errFetch{fmt.Sprintf("media url answered HTTP %d", resp.StatusCode)}
	}
	if resp.ContentLength > s.mediaMax {
		return nil, "", instance.ErrMediaTooLarge
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, s.mediaMax+1))
	if err != nil {
		return nil, "", errFetch{"could not download the media url"}
	}
	if int64(len(data)) > s.mediaMax {
		return nil, "", instance.ErrMediaTooLarge
	}
	return data, resp.Header.Get("Content-Type"), nil
}

func (s *Server) downloadMedia(w http.ResponseWriter, r *http.Request) {
	var med instance.Media
	if !decode(w, r, &med) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	f, err := s.manager.DownloadMedia(ctx, r.PathValue("id"), med, s.mediaMax)
	if err != nil {
		s.fail(w, err)
		return
	}
	defer f.Close()

	ct := "application/octet-stream"
	if base, params, err := mime.ParseMediaType(med.Mimetype); err == nil {
		ct = mime.FormatMediaType(base, params)
	}
	h := w.Header()
	h.Set("Content-Type", ct)
	h.Set("Content-Length", strconv.FormatInt(f.Size, 10))
	h.Set("X-Content-Type-Options", "nosniff")
	if name := strings.TrimSpace(med.FileName); name != "" {
		if cd := mime.FormatMediaType("attachment", map[string]string{"filename": name}); cd != "" {
			h.Set("Content-Disposition", cd)
		}
	}
	w.WriteHeader(http.StatusOK)
	if _, err := io.Copy(w, f); err != nil {
		s.log.Debug("media download: client went away", "error", err)
	}
}
