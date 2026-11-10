package instance

import (
	"context"
	"errors"
	"fmt"
	"mime"
	"os"
	"strings"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"
)

// Media errors returned to the HTTP layer.
var (
	ErrInvalidMedia  = errors.New("invalid media")
	ErrMediaTooLarge = errors.New("media is larger than the configured limit (MEDIA_MAX_MB)")
	// ErrMediaGone means WhatsApp no longer serves the file (media links expire after some
	// weeks, or the sender deleted it).
	ErrMediaGone = errors.New("the media is no longer available on WhatsApp servers")
)

// Media describes an attachment of a received message. It is sent in message.received, and
// the integration sends it back to POST /instances/{id}/media/download to get the file. The
// API keeps no copy of messages or files: everything needed to download is in this object
// (the []byte fields travel as base64).
type Media struct {
	Kind          string `json:"kind"` // image, video, audio, document or sticker
	Mimetype      string `json:"mimetype,omitempty"`
	FileName      string `json:"fileName,omitempty"`
	Size          uint64 `json:"size,omitempty"`
	Seconds       uint32 `json:"seconds,omitempty"` // audio and video
	PTT           bool   `json:"ptt,omitempty"`     // true for a voice note recorded in WhatsApp
	Width         uint32 `json:"width,omitempty"`
	Height        uint32 `json:"height,omitempty"`
	DirectPath    string `json:"directPath"`
	MediaKey      []byte `json:"mediaKey"`
	FileSHA256    []byte `json:"fileSha256"`
	FileEncSHA256 []byte `json:"fileEncSha256"`
}

// MediaOf returns the attachment of a message, or nil when it has none.
func MediaOf(m *waE2E.Message) *Media {
	switch {
	case m == nil:
		return nil
	case m.GetImageMessage() != nil:
		x := m.GetImageMessage()
		return &Media{Kind: "image", Mimetype: x.GetMimetype(), Size: x.GetFileLength(),
			Width: x.GetWidth(), Height: x.GetHeight(),
			DirectPath: x.GetDirectPath(), MediaKey: x.GetMediaKey(), FileSHA256: x.GetFileSHA256(), FileEncSHA256: x.GetFileEncSHA256()}
	case m.GetVideoMessage() != nil:
		x := m.GetVideoMessage()
		return &Media{Kind: "video", Mimetype: x.GetMimetype(), Size: x.GetFileLength(), Seconds: x.GetSeconds(),
			Width: x.GetWidth(), Height: x.GetHeight(),
			DirectPath: x.GetDirectPath(), MediaKey: x.GetMediaKey(), FileSHA256: x.GetFileSHA256(), FileEncSHA256: x.GetFileEncSHA256()}
	case m.GetAudioMessage() != nil:
		x := m.GetAudioMessage()
		return &Media{Kind: "audio", Mimetype: x.GetMimetype(), Size: x.GetFileLength(), Seconds: x.GetSeconds(), PTT: x.GetPTT(),
			DirectPath: x.GetDirectPath(), MediaKey: x.GetMediaKey(), FileSHA256: x.GetFileSHA256(), FileEncSHA256: x.GetFileEncSHA256()}
	case m.GetDocumentMessage() != nil:
		x := m.GetDocumentMessage()
		return &Media{Kind: "document", Mimetype: x.GetMimetype(), Size: x.GetFileLength(), FileName: x.GetFileName(),
			DirectPath: x.GetDirectPath(), MediaKey: x.GetMediaKey(), FileSHA256: x.GetFileSHA256(), FileEncSHA256: x.GetFileEncSHA256()}
	case m.GetStickerMessage() != nil:
		x := m.GetStickerMessage()
		return &Media{Kind: "sticker", Mimetype: x.GetMimetype(), Size: x.GetFileLength(),
			Width: x.GetWidth(), Height: x.GetHeight(),
			DirectPath: x.GetDirectPath(), MediaKey: x.GetMediaKey(), FileSHA256: x.GetFileSHA256(), FileEncSHA256: x.GetFileEncSHA256()}
	}
	return nil
}

// downloadable rebuilds, from a Media object sent by the integration, the message part that
// whatsmeow downloads and decrypts. The hashes make the download fail if anything was changed.
func downloadable(med Media) (whatsmeow.DownloadableMessage, error) {
	if !strings.HasPrefix(med.DirectPath, "/") || strings.ContainsAny(med.DirectPath, " \t\r\n\\#") ||
		len(med.MediaKey) != 32 || len(med.FileSHA256) != 32 || len(med.FileEncSHA256) != 32 {
		return nil, fmt.Errorf("%w: directPath, mediaKey, fileSha256 and fileEncSha256 must be the values received in message.received", ErrInvalidMedia)
	}
	dp, key, sha, enc := proto.String(med.DirectPath), med.MediaKey, med.FileSHA256, med.FileEncSHA256
	switch med.Kind {
	case "image":
		return &waE2E.ImageMessage{DirectPath: dp, MediaKey: key, FileSHA256: sha, FileEncSHA256: enc}, nil
	case "video":
		return &waE2E.VideoMessage{DirectPath: dp, MediaKey: key, FileSHA256: sha, FileEncSHA256: enc}, nil
	case "audio":
		return &waE2E.AudioMessage{DirectPath: dp, MediaKey: key, FileSHA256: sha, FileEncSHA256: enc}, nil
	case "document":
		return &waE2E.DocumentMessage{DirectPath: dp, MediaKey: key, FileSHA256: sha, FileEncSHA256: enc}, nil
	case "sticker":
		return &waE2E.StickerMessage{DirectPath: dp, MediaKey: key, FileSHA256: sha, FileEncSHA256: enc}, nil
	}
	return nil, fmt.Errorf("%w: kind must be image, video, audio, document or sticker", ErrInvalidMedia)
}

// MediaFile is a downloaded attachment in a temporary file. Close removes the file.
type MediaFile struct {
	*os.File
	Size int64
}

// Close closes and removes the temporary file.
func (f *MediaFile) Close() error {
	name := f.Name()
	err := f.File.Close()
	_ = os.Remove(name)
	return err
}

// cappedFile refuses to grow past max bytes. The size in the Media object comes from the
// caller and cannot be trusted, so the limit is enforced while WhatsApp's file is written.
type cappedFile struct {
	*os.File
	max int64
}

// The encrypted file is the plaintext padded to the AES block plus a 10-byte MAC.
const encryptionOverhead = 64

func (c cappedFile) Write(p []byte) (int, error) {
	if off, err := c.Seek(0, 1); err == nil && off+int64(len(p)) > c.max+encryptionOverhead {
		return 0, ErrMediaTooLarge
	}
	return c.File.Write(p)
}

func (c cappedFile) WriteAt(p []byte, off int64) (int, error) {
	if off+int64(len(p)) > c.max+encryptionOverhead {
		return 0, ErrMediaTooLarge
	}
	return c.File.WriteAt(p, off)
}

func (c cappedFile) Truncate(size int64) error {
	if size > c.max+encryptionOverhead {
		return ErrMediaTooLarge
	}
	return c.File.Truncate(size)
}

// DownloadMedia downloads and decrypts an attachment. The caller must Close the result.
func (m *Manager) DownloadMedia(ctx context.Context, id string, med Media, max int64) (*MediaFile, error) {
	msg, err := downloadable(med)
	if err != nil {
		return nil, err
	}
	if med.Size > uint64(max) {
		return nil, ErrMediaTooLarge
	}
	_, cli, err := m.connected(id)
	if err != nil {
		return nil, err
	}
	tmp, err := os.CreateTemp("", "firezap-media-*")
	if err != nil {
		return nil, err
	}
	out := &MediaFile{File: tmp}
	if err := cli.DownloadToFile(ctx, msg, cappedFile{File: tmp, max: max}); err != nil {
		_ = out.Close()
		switch {
		case errors.Is(err, ErrMediaTooLarge):
			return nil, ErrMediaTooLarge
		case errors.Is(err, whatsmeow.ErrMediaDownloadFailedWith403),
			errors.Is(err, whatsmeow.ErrMediaDownloadFailedWith404),
			errors.Is(err, whatsmeow.ErrMediaDownloadFailedWith410):
			return nil, ErrMediaGone
		case errors.Is(err, whatsmeow.ErrInvalidMediaHMAC),
			errors.Is(err, whatsmeow.ErrInvalidMediaEncSHA256),
			errors.Is(err, whatsmeow.ErrInvalidMediaSHA256),
			errors.Is(err, whatsmeow.ErrTooShortFile):
			return nil, fmt.Errorf("%w: the keys or hashes do not match the file", ErrInvalidMedia)
		}
		return nil, fmt.Errorf("media download failed: %w", err)
	}
	st, err := tmp.Stat()
	if err != nil {
		_ = out.Close()
		return nil, err
	}
	if st.Size() > max {
		_ = out.Close()
		return nil, ErrMediaTooLarge
	}
	out.Size = st.Size()
	if _, err := tmp.Seek(0, 0); err != nil {
		_ = out.Close()
		return nil, err
	}
	return out, nil
}

// OutgoingMedia is an attachment to send.
type OutgoingMedia struct {
	Kind     string // image, video, audio or document
	Data     []byte
	Mimetype string
	FileName string // documents
	Caption  string // image, video and document
	PTT      bool   // audio: send as a voice note (needs OGG/Opus)
}

// mediaTypes maps an outgoing kind to whatsmeow's upload type.
var mediaTypes = map[string]whatsmeow.MediaType{
	"image":    whatsmeow.MediaImage,
	"video":    whatsmeow.MediaVideo,
	"audio":    whatsmeow.MediaAudio,
	"document": whatsmeow.MediaDocument,
}

// baseMime returns the mimetype without parameters, lowercase ("audio/ogg; codecs=opus" -> "audio/ogg").
func baseMime(mt string) string {
	if base, _, err := mime.ParseMediaType(mt); err == nil {
		return strings.ToLower(base)
	}
	return strings.ToLower(strings.TrimSpace(strings.SplitN(mt, ";", 2)[0]))
}

// prepareOutgoing checks an attachment and normalizes its mimetype. WhatsApp shows images,
// videos and voice notes only in the formats its apps play; anything else must go as a
// document, so a wrong type is refused here instead of arriving broken on the phone.
func prepareOutgoing(out *OutgoingMedia) error {
	if _, ok := mediaTypes[out.Kind]; !ok {
		return fmt.Errorf("%w: type must be image, video, audio or document", ErrInvalidMedia)
	}
	if len(out.Data) == 0 {
		return fmt.Errorf("%w: the file is empty", ErrInvalidMedia)
	}
	base := baseMime(out.Mimetype)
	switch out.Kind {
	case "image":
		if base != "image/jpeg" && base != "image/png" {
			return fmt.Errorf("%w: images must be JPEG or PNG (got %q); send other formats as a document", ErrInvalidMedia, base)
		}
	case "video":
		if base != "video/mp4" && base != "video/3gpp" {
			return fmt.Errorf("%w: videos must be MP4 or 3GP (got %q); send other formats as a document", ErrInvalidMedia, base)
		}
	case "audio":
		if out.PTT {
			if base != "audio/ogg" && base != "application/ogg" && base != "audio/opus" {
				return fmt.Errorf("%w: voice notes (ptt) must be OGG/Opus (got %q)", ErrInvalidMedia, base)
			}
			out.Mimetype = "audio/ogg; codecs=opus"
		} else if !strings.HasPrefix(base, "audio/") && base != "application/ogg" {
			return fmt.Errorf("%w: audio must have an audio/* type (got %q)", ErrInvalidMedia, base)
		}
	case "document":
		if base == "" {
			out.Mimetype = "application/octet-stream"
		}
		if strings.TrimSpace(out.FileName) == "" {
			out.FileName = "file"
		}
	}
	return nil
}

// buildMediaMessage turns an uploaded attachment into the message that references it.
func buildMediaMessage(out OutgoingMedia, up whatsmeow.UploadResponse) *waE2E.Message {
	var caption *string
	if out.Caption != "" {
		caption = proto.String(out.Caption)
	}
	url, dp, size := proto.String(up.URL), proto.String(up.DirectPath), proto.Uint64(up.FileLength)
	mt := proto.String(out.Mimetype)
	switch out.Kind {
	case "image":
		return &waE2E.Message{ImageMessage: &waE2E.ImageMessage{Caption: caption, Mimetype: mt, URL: url, DirectPath: dp,
			MediaKey: up.MediaKey, FileEncSHA256: up.FileEncSHA256, FileSHA256: up.FileSHA256, FileLength: size}}
	case "video":
		return &waE2E.Message{VideoMessage: &waE2E.VideoMessage{Caption: caption, Mimetype: mt, URL: url, DirectPath: dp,
			MediaKey: up.MediaKey, FileEncSHA256: up.FileEncSHA256, FileSHA256: up.FileSHA256, FileLength: size}}
	case "audio":
		return &waE2E.Message{AudioMessage: &waE2E.AudioMessage{Mimetype: mt, PTT: proto.Bool(out.PTT), URL: url, DirectPath: dp,
			MediaKey: up.MediaKey, FileEncSHA256: up.FileEncSHA256, FileSHA256: up.FileSHA256, FileLength: size}}
	default:
		return &waE2E.Message{DocumentMessage: &waE2E.DocumentMessage{Caption: caption, Mimetype: mt, FileName: proto.String(out.FileName),
			Title: proto.String(out.FileName), URL: url, DirectPath: dp,
			MediaKey: up.MediaKey, FileEncSHA256: up.FileEncSHA256, FileSHA256: up.FileSHA256, FileLength: size}}
	}
}

// SendMedia uploads an attachment and sends it. It returns the WhatsApp message ID.
func (m *Manager) SendMedia(ctx context.Context, id, to string, out OutgoingMedia) (string, error) {
	jid, err := ParseRecipient(to)
	if err != nil {
		return "", err
	}
	if err := prepareOutgoing(&out); err != nil {
		return "", err
	}
	rt, cli, err := m.connected(id)
	if err != nil {
		return "", err
	}
	up, err := cli.Upload(ctx, out.Data, mediaTypes[out.Kind])
	if err != nil {
		return "", fmt.Errorf("media upload failed: %w", err)
	}
	return m.paced(ctx, rt, cli, jid, buildMediaMessage(out, up))
}
