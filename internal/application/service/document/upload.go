package document

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/thanhenti/bepaylot/internal/application/repository/postgres"
	"github.com/thanhenti/bepaylot/internal/application/service/metadata"
	"github.com/thanhenti/bepaylot/internal/parser/pdf"
	"github.com/thanhenti/bepaylot/internal/queue"
	"github.com/thanhenti/bepaylot/internal/types"
)

// Upload is one (possibly multi-file) upload request. Files are streamed to
// S3 as they arrive; metadata may arrive before or after the files and is
// validated in Finish, which also enqueues the pipeline.
type Upload struct {
	s           *Service
	owner       uuid.UUID
	kb          types.KnowledgeBase
	interactive bool
	batch       types.UploadBatch

	shared   map[string]any
	perFile  map[string]map[string]any
	accepted []pendingDoc
	rejected []Rejected
	index    int
}

type pendingDoc struct {
	doc       types.Document
	index     int
	duplicate bool
}

// Rejected describes a file that was not accepted.
type Rejected struct {
	FileName string   `json:"file_name"`
	Index    int      `json:"index"`
	Errors   []string `json:"errors"`
}

// Accepted describes an accepted file.
type Accepted struct {
	DocumentID uuid.UUID      `json:"document_id"`
	FileName   string         `json:"file_name"`
	Status     string         `json:"status"`
	Duplicate  bool           `json:"duplicate,omitempty"`
	Metadata   map[string]any `json:"metadata,omitempty"`
}

// UploadResult is returned by Finish.
type UploadResult struct {
	BatchID   uuid.UUID  `json:"batch_id"`
	Documents []Accepted `json:"documents"`
	Rejected  []Rejected `json:"rejected"`
}

// BeginUpload starts an upload into a KB the owner owns.
func (s *Service) BeginUpload(ctx context.Context, owner, kbID uuid.UUID, interactive bool) (*Upload, error) {
	kb, err := s.st.KBs.GetOwned(ctx, kbID, owner)
	if errors.Is(err, postgres.ErrNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &Upload{s: s, owner: owner, kb: kb, interactive: interactive, perFile: map[string]map[string]any{}}, nil
}

// SetSharedMetadata sets metadata applied to every file of the batch.
func (u *Upload) SetSharedMetadata(m map[string]any) { u.shared = m }

// SetFilesMetadata sets per-file metadata keyed by file name or 0-based index.
func (u *Upload) SetFilesMetadata(m map[string]map[string]any) {
	for k, v := range m {
		u.perFile[k] = v
	}
}

// AddFile streams one file to S3 and records its document row. Per-file
// problems (type, size) are recorded as rejections, not returned.
func (u *Upload) AddFile(ctx context.Context, name, declaredType string, body io.Reader) error {
	idx := u.index
	u.index++
	if u.index > u.s.cfg.Upload.MaxFiles {
		u.reject(name, idx, fmt.Sprintf("too many files (max %d)", u.s.cfg.Upload.MaxFiles))
		_, _ = io.Copy(io.Discard, body)
		return nil
	}
	name = filepath.Base(strings.TrimSpace(name))
	if name == "" || name == "." {
		name = fmt.Sprintf("file-%d", idx+1)
	}

	br := bufio.NewReaderSize(body, 4096)
	head, _ := br.Peek(512)
	mime := sniff(head, declaredType)
	if !u.s.allowed(mime) {
		u.reject(name, idx, fmt.Sprintf("unsupported file type %q", mime))
		_, _ = io.Copy(io.Discard, br)
		return nil
	}

	docID := uuid.New()
	key := u.s.keys.Source(u.kb.ID, docID, extFor(mime, name))
	hash := sha256.New()
	var det pdf.PDFADetector
	limited := &limitReader{r: io.TeeReader(br, io.MultiWriter(hash, &det)), max: u.s.cfg.Upload.MaxBytes}
	info, err := u.s.objects.Put(ctx, key, limited, -1, mime)
	if limited.exceeded {
		_ = u.s.objects.Delete(context.WithoutCancel(ctx), key)
		u.reject(name, idx, fmt.Sprintf("file exceeds %d bytes", u.s.cfg.Upload.MaxBytes))
		_, _ = io.Copy(io.Discard, br)
		return nil
	}
	if err != nil {
		return fmt.Errorf("upload %s: %w", name, err)
	}

	if u.batch.ID == uuid.Nil {
		b, err := u.s.st.KBs.CreateBatch(ctx, types.UploadBatch{KBID: u.kb.ID, CreatedBy: u.owner, Metadata: u.shared})
		if err != nil {
			return err
		}
		u.batch = b
	}
	d := types.Document{
		ID: docID, KBID: u.kb.ID, BatchID: &u.batch.ID, CreatedBy: u.owner, FileName: name, MimeType: mime,
		SizeBytes: limited.n, SHA256: hex.EncodeToString(hash.Sum(nil)), StorageKey: info.Key,
		Engine: firstNonEmpty(u.kb.Config.ParserEngine, u.s.cfg.Parser.DefaultEngine),
	}
	if det.IsPDFA() {
		part := det.Part
		d.PDFAPart, d.PDFAConformance = &part, det.Conform
	}
	created, err := u.s.st.Documents.Create(ctx, d, u.interactive)
	if errors.Is(err, postgres.ErrDuplicate) {
		_ = u.s.objects.Delete(context.WithoutCancel(ctx), key)
		u.accepted = append(u.accepted, pendingDoc{doc: created, index: idx, duplicate: true})
		return nil
	}
	if err != nil {
		_ = u.s.objects.Delete(context.WithoutCancel(ctx), key)
		return err
	}
	u.accepted = append(u.accepted, pendingDoc{doc: created, index: idx})
	return nil
}

// Finish validates metadata per file, stores it and enqueues the pipeline.
// Files whose metadata is invalid are rejected and removed.
func (u *Upload) Finish(ctx context.Context) (*UploadResult, error) {
	res := &UploadResult{BatchID: u.batch.ID, Rejected: append([]Rejected{}, u.rejected...), Documents: []Accepted{}}
	for _, p := range u.accepted {
		own := u.perFile[p.doc.FileName]
		if own == nil {
			own = u.perFile[strconv.Itoa(p.index)]
		}
		if p.duplicate {
			res.Documents = append(res.Documents, Accepted{DocumentID: p.doc.ID, FileName: p.doc.FileName, Status: p.doc.Status, Duplicate: true, Metadata: p.doc.Metadata})
			continue
		}
		meta, errs := metadata.Validate(u.kb.MetadataSchema, metadata.Merge(u.shared, own))
		if len(errs) > 0 {
			msgs := make([]string, len(errs))
			for i, e := range errs {
				msgs[i] = e.Error()
			}
			res.Rejected = append(res.Rejected, Rejected{FileName: p.doc.FileName, Index: p.index, Errors: msgs})
			_ = u.s.objects.Delete(context.WithoutCancel(ctx), p.doc.StorageKey)
			_ = u.s.st.Documents.Purge(context.WithoutCancel(ctx), p.doc.ID)
			continue
		}
		doc, err := u.s.st.Documents.SetMetadata(ctx, p.doc.ID, meta)
		if err != nil {
			return nil, err
		}
		if err := u.s.enqueue(ctx, types.TaskDocumentSplit, doc, nil, fmt.Sprintf("split:%s:%d", doc.ID, doc.Gen)); err != nil {
			return nil, err
		}
		res.Documents = append(res.Documents, Accepted{DocumentID: doc.ID, FileName: doc.FileName, Status: doc.Status, Metadata: doc.Metadata})
	}
	if u.batch.ID != uuid.Nil {
		_ = u.s.st.KBs.FinishBatch(ctx, u.batch.ID, len(res.Documents), len(res.Rejected))
	}
	return res, nil
}

func (u *Upload) reject(name string, idx int, msg string) {
	u.rejected = append(u.rejected, Rejected{FileName: name, Index: idx, Errors: []string{msg}})
}

func (s *Service) allowed(mime string) bool {
	for _, t := range s.cfg.Upload.AllowedTypes {
		if strings.EqualFold(t, mime) {
			return true
		}
	}
	return false
}

// enqueue sends a per-document task, choosing the interactive lane when the
// document came from chat.
func (s *Service) enqueue(ctx context.Context, taskType string, d types.Document, pages []int, taskID string) error {
	return s.q.Enqueue(ctx, taskType, types.DocTaskPayload{DocumentID: d.ID, KBID: d.KBID, Gen: d.Gen, Interactive: d.Interactive, Pages: pages},
		queue.Opts{TaskID: taskID, Interactive: d.Interactive})
}

func sniff(head []byte, declared string) string {
	switch {
	case bytes.HasPrefix(head, []byte("%PDF-")):
		return "application/pdf"
	case bytes.HasPrefix(head, []byte("II*\x00")), bytes.HasPrefix(head, []byte("MM\x00*")):
		return "image/tiff"
	}
	if ct := http.DetectContentType(head); ct != "application/octet-stream" {
		return strings.Split(ct, ";")[0]
	}
	if declared != "" {
		return strings.Split(declared, ";")[0]
	}
	return "application/octet-stream"
}

func extFor(mime, name string) string {
	switch mime {
	case "application/pdf":
		return "pdf"
	case "image/jpeg":
		return "jpg"
	case "image/png":
		return "png"
	case "image/tiff":
		return "tif"
	}
	return strings.TrimPrefix(filepath.Ext(name), ".")
}

type limitReader struct {
	r        io.Reader
	n, max   int64
	exceeded bool
}

func (l *limitReader) Read(p []byte) (int, error) {
	n, err := l.r.Read(p)
	l.n += int64(n)
	if l.max > 0 && l.n > l.max {
		l.exceeded = true
		return n, ErrTooLarge
	}
	return n, err
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}
