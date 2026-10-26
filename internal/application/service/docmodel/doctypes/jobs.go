package doctypes

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/thanhenti/bepaylot/docpipe/textutil"
	dtypes "github.com/thanhenti/bepaylot/docpipe/types"
	"github.com/thanhenti/bepaylot/internal/application/repository/postgres"
	"github.com/thanhenti/bepaylot/internal/application/service/docmodel"
	"github.com/thanhenti/bepaylot/internal/llm"
	"github.com/thanhenti/bepaylot/internal/queue"
	"github.com/thanhenti/bepaylot/internal/types"
)

// Run runs a version (0 = the one in use) on samples of a type (none =
// every sample that is read) and returns the finished job. The user waits
// on screen, so it runs in the request, not through the queue: the job row
// only tells other views that a run is going on. Each sample's agent turn
// is bounded by doc_types.run_timeout; a closed page does not stop it.
func (s *Service) Run(ctx context.Context, owner uuid.UUID, code string, ids []uuid.UUID, version int, by uuid.UUID) (types.DocTypeJob, error) {
	p, err := s.st.DocTypes.Prompt(ctx, owner, code, version)
	if err != nil {
		return types.DocTypeJob{}, notFound(err)
	}
	// Without fields there is nothing to ask: the model's reply would not
	// parse and the job would retry for hours.
	if len(p.Fields) == 0 {
		return types.DocTypeJob{}, fmt.Errorf("%w: chưa có trường nào, hãy thêm trường cần lấy trước khi chạy thử", ErrBad)
	}
	samples, err := s.st.DocTypes.Samples(ctx, owner, code)
	if err != nil {
		return types.DocTypeJob{}, err
	}
	var pick []uuid.UUID
	for _, smp := range samples {
		if smp.Status == types.SampleReady && (len(ids) == 0 || slices.Contains(ids, smp.ID)) {
			pick = append(pick, smp.ID)
		}
	}
	if len(pick) == 0 {
		return types.DocTypeJob{}, fmt.Errorf("%w: no sample that is read", ErrBad)
	}
	if s.extract == nil {
		return types.DocTypeJob{}, ErrNotReady
	}
	j, err := s.st.DocTypes.CreateJob(ctx, owner, types.DocTypeJob{Code: code, Kind: types.JobRun, Version: p.Version, SampleIDs: pick, CreatedBy: by})
	if err != nil {
		return j, err
	}
	rctx := llm.WithCaller(context.WithoutCancel(ctx), llm.Caller{UserID: &by, Kind: types.UsageParse})
	msg := ""
	if err := s.runJob(rctx, owner, j); err != nil {
		s.log.Warn("doctypes: run failed", "job", j.ID, "err", err)
		msg = failReason(err)
	}
	if err := s.st.DocTypes.FinishJob(rctx, j.ID, nil, msg); err != nil {
		return j, err
	}
	return s.Job(rctx, owner, code, j.ID)
}

// Optimize queues an AI proposal that improves a version (U98); it needs a
// reviewed sample.
func (s *Service) Optimize(ctx context.Context, owner uuid.UUID, code string, version int, by uuid.UUID) (types.DocTypeJob, error) {
	p, err := s.st.DocTypes.Prompt(ctx, owner, code, version)
	if err != nil {
		return types.DocTypeJob{}, notFound(err)
	}
	samples, err := s.st.DocTypes.Samples(ctx, owner, code)
	if err != nil {
		return types.DocTypeJob{}, err
	}
	if !slices.ContainsFunc(samples, func(x types.DocTypeSample) bool { return x.Truth != nil && x.Status == types.SampleReady }) {
		return types.DocTypeJob{}, ErrNoReviewed
	}
	return s.startJob(ctx, owner, types.DocTypeJob{Code: code, Kind: types.JobOptimize, Version: p.Version, CreatedBy: by})
}

func (s *Service) startJob(ctx context.Context, owner uuid.UUID, j types.DocTypeJob) (types.DocTypeJob, error) {
	if s.llm == nil || s.extract == nil {
		return j, ErrNotReady
	}
	j, err := s.st.DocTypes.CreateJob(ctx, owner, j)
	if err != nil {
		return j, err
	}
	if err := s.q.Enqueue(ctx, types.TaskDocTypeJob, jobPayload{JobID: j.ID}, queue.Opts{Interactive: true}); err != nil {
		_ = s.st.DocTypes.FinishJob(context.WithoutCancel(ctx), j.ID, nil, "could not queue: "+err.Error())
		return j, err
	}
	return j, nil
}

// Job returns a job of a type.
func (s *Service) Job(ctx context.Context, owner uuid.UUID, code string, id uuid.UUID) (types.DocTypeJob, error) {
	j, err := s.st.DocTypes.Job(ctx, owner, id)
	if err != nil || j.Code != code {
		return j, ErrNotFound
	}
	return j, nil
}

// Jobs returns the running jobs of a type and the latest finished one of
// each kind. A test run still "running" well past its time limit was cut
// off (server restarted mid-request): it is closed, so the page stops
// waiting for it.
func (s *Service) Jobs(ctx context.Context, owner uuid.UUID, code string) ([]types.DocTypeJob, error) {
	jobs, err := s.st.DocTypes.Jobs(ctx, owner, code)
	if err != nil {
		return nil, err
	}
	stale := false
	for _, j := range jobs {
		if j.Kind == types.JobRun && j.Status == types.JobRunning && time.Since(j.CreatedAt) > s.runLimit(len(j.SampleIDs))+time.Minute {
			if err := s.st.DocTypes.FinishJob(ctx, j.ID, nil, "Lần chạy thử bị gián đoạn, hãy chạy lại"); err != nil {
				return nil, err
			}
			stale = true
		}
	}
	if stale {
		return s.st.DocTypes.Jobs(ctx, owner, code)
	}
	return jobs, nil
}

// runParallel is how many samples of a test run are read at once.
const runParallel = 4

// runTimeout bounds one sample's extraction turn (doc_types.run_timeout).
func (s *Service) runTimeout() time.Duration {
	if s.cfg == nil || s.cfg.DocTypes.RunTimeout <= 0 {
		return 60 * time.Second
	}
	return s.cfg.DocTypes.RunTimeout
}

// runLimit is the longest a test run on n samples can take.
func (s *Service) runLimit(n int) time.Duration {
	return time.Duration((n+runParallel-1)/runParallel) * s.runTimeout()
}

// DropProposal forgets an optimization's proposal.
func (s *Service) DropProposal(ctx context.Context, owner uuid.UUID, code string, id uuid.UUID) error {
	if _, err := s.Job(ctx, owner, code, id); err != nil {
		return err
	}
	return s.st.DocTypes.DropProposal(ctx, owner, id)
}

type jobPayload struct {
	JobID uuid.UUID `json:"job_id"`
}

func (s *Service) handleJob(ctx context.Context, payload []byte) error {
	var p jobPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return fmt.Errorf("%w: %v", queue.ErrSkipRetry, err)
	}
	j, owner, err := s.st.DocTypes.JobByID(ctx, p.JobID)
	if errors.Is(err, postgres.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if j.Status != types.JobRunning {
		return nil
	}
	ctx = llm.WithCaller(ctx, llm.Caller{UserID: &j.CreatedBy, Kind: types.UsageParse})
	var result *types.OptimizeProposal
	if j.Kind == types.JobOptimize {
		result, err = s.optimize(ctx, owner, j)
	} else {
		err = s.runJob(ctx, owner, j)
	}
	if err != nil {
		if !errors.Is(err, queue.ErrSkipRetry) && !authFailed(err) && !queue.IsFinalAttempt(ctx) {
			return err
		}
		s.log.Warn("doctypes: job failed", "job", j.ID, "kind", j.Kind, "err", err)
		return s.st.DocTypes.FinishJob(context.WithoutCancel(ctx), j.ID, nil, failReason(err))
	}
	return s.st.DocTypes.FinishJob(ctx, j.ID, result, "")
}

// runJob runs a version on the job's samples and stores the outputs.
func (s *Service) runJob(ctx context.Context, owner uuid.UUID, j types.DocTypeJob) error {
	t, err := s.st.DocTypes.Get(ctx, owner, j.Code)
	if err != nil {
		return fmt.Errorf("%w: %v", queue.ErrSkipRetry, err)
	}
	p, err := s.st.DocTypes.Prompt(ctx, owner, j.Code, j.Version)
	if err != nil {
		return fmt.Errorf("%w: %v", queue.ErrSkipRetry, err)
	}
	if len(p.Fields) == 0 { // a job queued before Run refused this
		return fmt.Errorf("%w: version %d has no fields", queue.ErrSkipRetry, p.Version)
	}
	// Samples are read runParallel at a time; one that fails does not stop
	// the others, whose results are kept.
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		errs []error
		sem  = make(chan struct{}, runParallel)
	)
	for _, id := range j.SampleIDs {
		wg.Add(1)
		sem <- struct{}{}
		go func(id uuid.UUID) {
			defer func() { <-sem; wg.Done() }()
			if err := s.runSample(ctx, owner, j.CreatedBy, t, p, id); err != nil {
				mu.Lock()
				errs = append(errs, err)
				mu.Unlock()
			}
		}(id)
	}
	wg.Wait()
	if len(errs) > 0 {
		return errs[0]
	}
	return nil
}

func (s *Service) runSample(ctx context.Context, owner, by uuid.UUID, t types.CatalogDocType, p types.DocTypePrompt, id uuid.UUID) error {
	smp, err := s.st.DocTypes.Sample(ctx, owner, id)
	if errors.Is(err, postgres.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	out, err := s.runOne(ctx, owner, by, t, p, smp)
	if err != nil {
		return err
	}
	return s.st.DocTypes.SaveRun(ctx, types.SampleRun{SampleID: id, Version: p.Version, Output: out})
}

// runOne is one extraction turn of a version on a sample, as when a sheet
// is built (§6.9.3): the same message (page content and lines of the
// sample) goes to the same agent turn, run as the user who started the job,
// without the sample among the examples. Replies are cached by the text
// sent, so running a version again costs nothing.
func (s *Service) runOne(ctx context.Context, owner, by uuid.UUID, t types.CatalogDocType, p types.DocTypePrompt, smp types.DocTypeSample) (map[string]types.RunValue, error) {
	g := docmodel.GuideOf(ctx, s.st, owner, p, smp.ID)
	var lines []postgres.PageLine
	md := map[int]string{}
	size := map[int][2]int{}
	for _, pg := range smp.Pages {
		md[pg.N] = pg.Markdown
		size[pg.N] = [2]int{int(pg.Width), int(pg.Height)}
		for _, l := range pg.Lines {
			lines = append(lines, postgres.PageLine{PageNo: pg.N, ParsedLine: dtypes.ParsedLine{LineNo: l.N, Text: l.Text, Confidence: l.Conf, BBox: dtypes.BBoxFromArray(l.BBox)}})
		}
	}
	text := docmodel.ExtractPrompt(tableOf(t, p), lines, md, size, s.minConf(), g)
	ctx, cancel := context.WithTimeout(ctx, s.runTimeout())
	defer cancel()
	reply, err := s.askExtract(ctx, by, smp.ID, text)
	if err != nil {
		return nil, err
	}
	out, ok := docmodel.RunValues(p.Fields, reply)
	if !ok {
		// Asked again at once: a retry through the queue waits 15 s or more
		// while the user watches the run.
		s.log.Warn("doctypes: the reply has no JSON, asking again", "sample", smp.ID, "reply", textutil.Truncate(reply, 500))
		if reply, err = s.askExtract(ctx, by, smp.ID, text+jsonOnly); err != nil {
			return nil, err
		}
		if out, ok = docmodel.RunValues(p.Fields, reply); !ok {
			s.log.Warn("doctypes: the reply has no JSON", "sample", smp.ID, "reply", textutil.Truncate(reply, 500))
			return nil, errNotJSON
		}
	}
	return out, nil
}

// jsonOnly ends the message of the second ask, after a reply without JSON.
const jsonOnly = "\n\nChỉ trả lời đúng một JSON {\"fields\":[…]} như mẫu ở trên, không viết thêm gì."

var errNotJSON = errors.New("the reply is not JSON")

// askExtract runs the extraction turn of the agent on text, through
// model_cache (U56); only a reply that parses is cached.
func (s *Service) askExtract(ctx context.Context, by, sample uuid.UUID, text string) (string, error) {
	sum := sha256.Sum256([]byte(text))
	key := "doctype-run:" + hex.EncodeToString(sum[:])
	if b, ok, err := s.st.ModelCache.Get(ctx, key); err == nil && ok {
		var reply string
		if json.Unmarshal(b, &reply) == nil {
			return reply, nil
		}
	}
	u, err := s.st.Users.Get(ctx, by)
	if err != nil {
		return "", fmt.Errorf("%w: user %s: %v", queue.ErrSkipRetry, by, err)
	}
	reply, err := s.extract.RunExtract(ctx, u, uuid.Nil, sample, text)
	if err != nil {
		return "", err
	}
	if _, ok := docmodel.RunValues(nil, reply); ok {
		if b, err := json.Marshal(reply); err == nil {
			_ = s.st.ModelCache.Put(ctx, key, "doctype", b)
		}
	}
	return reply, nil
}

// failReason tells the user, in plain words, why a job did not finish.
func failReason(err error) string {
	m := strings.ToLower(err.Error())
	switch {
	case authFailed(err):
		return "Khoá AI không hợp lệ hoặc đã hết hạn, kiểm tra khoá AI trong Cài đặt"
	case strings.Contains(m, "429") || strings.Contains(m, "rate limit") || strings.Contains(m, "quota"):
		return "AI đang quá tải hoặc đã hết hạn mức, thử lại sau"
	case errors.Is(err, errNotJSON):
		return "AI trả lời không đúng dạng, thử chạy lại"
	case errors.Is(err, context.DeadlineExceeded) || strings.Contains(m, "timeout") || strings.Contains(m, "deadline"):
		return "AI trả lời quá lâu, đã dừng sau thời gian cho phép, thử lại sau"
	}
	return "AI chưa trả lời được, thử lại sau"
}

// authFailed: the provider refused the key; retrying cannot help.
func authFailed(err error) bool {
	m := strings.ToLower(err.Error())
	return strings.Contains(m, "status code: 401") || strings.Contains(m, "status code: 403") || strings.Contains(m, "invalid api key")
}

// complete calls the model, through model_cache (U56).
func (s *Service) complete(ctx context.Context, system, user string, out any) error {
	sum := sha256.Sum256([]byte(system + "\n" + user))
	key := "doctype:" + hex.EncodeToString(sum[:])
	if b, ok, err := s.st.ModelCache.Get(ctx, key); err == nil && ok {
		if json.Unmarshal(b, out) == nil {
			return nil
		}
	}
	if err := s.llm.CompleteJSON(ctx, system, user, out); err != nil {
		return err
	}
	if b, err := json.Marshal(out); err == nil {
		_ = s.st.ModelCache.Put(ctx, key, "doctype", b)
	}
	return nil
}

// miss is a wrong cell of a version: what it read and the reviewed value.
type miss struct {
	sample uuid.UUID
	file   string
	key    string
	got    string
	truth  string
	line   string
}

const optimizeSystem = `Bạn giúp cải thiện hướng dẫn cho AI đọc một loại giấy tờ. Bạn nhận hướng dẫn hiện tại, các trường cần lấy và các lần AI đọc sai trên những mẫu đã được người dùng duyệt (giá trị AI đọc và giá trị đúng).
Tìm các lỗi lặp lại hoặc rõ ràng. Với mỗi trường hay sai, viết MỘT quy tắc ngắn bằng tiếng Việt, lời thường cho người nghiệp vụ đọc được (không cú pháp kỹ thuật), nói cách tìm hoặc cách ghi đúng giá trị. Không viết quy tắc chỉ đúng cho một mẫu (ví dụ chép lại giá trị đúng).
Chọn tối đa 2 mẫu nên làm ví dụ cho AI (mẫu phủ nhiều lỗi nhất) và nói vì sao.
Không có lỗi lặp lại rõ ràng thì trả mảng rỗng.
Trả đúng một JSON: {"patterns":[{"field":"<key>","finding":"<lỗi thấy được>","rule":"<quy tắc>"}],"shots":[{"sample":"<id mẫu>","why":"<vì sao>"}]}`

// optimize asks the model for rules and examples that fix the errors of a
// version, builds the proposed version and scores both on the reviewed
// samples (an example is scored without itself, U98).
func (s *Service) optimize(ctx context.Context, owner uuid.UUID, j types.DocTypeJob) (*types.OptimizeProposal, error) {
	t, err := s.st.DocTypes.Get(ctx, owner, j.Code)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", queue.ErrSkipRetry, err)
	}
	p, err := s.st.DocTypes.Prompt(ctx, owner, j.Code, j.Version)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", queue.ErrSkipRetry, err)
	}
	samples, runs, err := s.scoring(ctx, owner, j.Code)
	if err != nil {
		return nil, err
	}
	var reviewed []types.DocTypeSample
	before := map[uuid.UUID]map[string]types.RunValue{}
	for _, smp := range samples {
		if smp.Truth == nil || smp.Status != types.SampleReady {
			continue
		}
		reviewed = append(reviewed, smp)
		if r, ok := runs[smp.ID][p.Version]; ok {
			before[smp.ID] = r.Output
			continue
		}
		out, err := s.runOne(ctx, owner, j.CreatedBy, t, p, smp)
		if err != nil {
			return nil, err
		}
		if err := s.st.DocTypes.SaveRun(ctx, types.SampleRun{SampleID: smp.ID, Version: p.Version, Output: out}); err != nil {
			return nil, err
		}
		before[smp.ID] = out
	}
	var misses []miss
	for _, smp := range reviewed {
		for _, f := range p.Fields {
			got := before[smp.ID][f.Key]
			if textutil.ValueKey(got.Value) == textutil.ValueKey(smp.Truth[f.Key]) {
				continue
			}
			m := miss{sample: smp.ID, file: smp.FileName, key: f.Key, got: got.Value, truth: smp.Truth[f.Key]}
			for _, pg := range smp.Pages {
				for _, l := range pg.Lines {
					if pg.N == got.Page && l.N == got.Line {
						m.line = l.Text
					}
				}
			}
			misses = append(misses, m)
		}
	}
	none := &types.OptimizeProposal{None: true, Version: p.Version}
	if len(misses) == 0 {
		return none, nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Loại giấy tờ: %s\n\nHướng dẫn hiện tại:\n%s\n\nCác trường:\n", t.Title, p.Instruction)
	for _, f := range p.Fields {
		fmt.Fprintf(&b, "- %s (key %s, kiểu %s)%s\n", f.Label, f.Key, f.ValueType, map[bool]string{true: ": " + f.Description}[f.Description != ""])
	}
	if len(p.Rules) > 0 {
		b.WriteString("\nQuy tắc đang có:\n")
		for _, r := range p.Rules {
			fmt.Fprintf(&b, "- %s\n", r)
		}
	}
	b.WriteString("\nCác mẫu đã duyệt:\n")
	for _, smp := range reviewed {
		fmt.Fprintf(&b, "- id %s: %s\n", smp.ID, smp.FileName)
	}
	b.WriteString("\nCác lần đọc sai:\n")
	for _, m := range misses {
		fmt.Fprintf(&b, "- mẫu %s (%s), trường %s: AI đọc %q, đúng là %q", m.sample, m.file, m.key, m.got, m.truth)
		if m.line != "" {
			fmt.Fprintf(&b, " (dòng AI dựa vào: %q)", m.line)
		}
		b.WriteByte('\n')
	}
	var ans struct {
		Patterns []struct {
			Field   string `json:"field"`
			Finding string `json:"finding"`
			Rule    string `json:"rule"`
		} `json:"patterns"`
		Shots []struct {
			Sample string `json:"sample"`
			Why    string `json:"why"`
		} `json:"shots"`
	}
	if err := s.complete(ctx, optimizeSystem, b.String(), &ans); err != nil {
		return nil, err
	}

	np := p
	np.Rules = slices.Clone(p.Rules)
	np.Shots = slices.Clone(p.Shots)
	res := &types.OptimizeProposal{Version: p.Version, Instruction: p.Instruction, Fields: p.Fields, Patterns: []types.OptimizePattern{}}
	hasField := func(k string) bool {
		return slices.ContainsFunc(p.Fields, func(f types.SheetField) bool { return f.Key == k })
	}
	for _, x := range ans.Patterns {
		rule := strings.TrimSpace(x.Rule)
		if !hasField(x.Field) || rule == "" || slices.Contains(np.Rules, rule) {
			continue
		}
		np.Rules = append(np.Rules, rule)
		pat := types.OptimizePattern{Field: x.Field, Finding: strings.TrimSpace(x.Finding), Rule: rule, Samples: []uuid.UUID{}}
		for _, m := range misses {
			if m.key == x.Field {
				pat.Samples = append(pat.Samples, m.sample)
			}
		}
		res.Patterns = append(res.Patterns, pat)
	}
	newShots := 0
	for _, x := range ans.Shots {
		id, err := uuid.Parse(strings.TrimSpace(x.Sample))
		if err != nil || newShots >= 2 || !slices.ContainsFunc(reviewed, func(r types.DocTypeSample) bool { return r.ID == id }) ||
			slices.ContainsFunc(np.Shots, func(sh types.PromptShot) bool { return sh.SampleID == id }) {
			continue
		}
		np.Shots = append(np.Shots, types.PromptShot{SampleID: id, Why: strings.TrimSpace(x.Why)})
		newShots++
	}
	if len(res.Patterns) == 0 && newShots == 0 {
		return none, nil
	}
	res.Rules, res.Shots = np.Rules, np.Shots

	after := map[uuid.UUID]map[string]types.RunValue{}
	for _, smp := range reviewed {
		out, err := s.runOne(ctx, owner, j.CreatedBy, t, np, smp)
		if err != nil {
			return nil, err
		}
		after[smp.ID] = out
	}
	score := func(outs map[uuid.UUID]map[string]types.RunValue, only string) *float64 {
		var cells []cell
		for _, smp := range reviewed {
			cells = append(cells, cellsOf(p.Fields, smp.Truth, outs[smp.ID], only)...)
		}
		return accuracy(cells)
	}
	res.Before, res.After = score(before, ""), score(after, "")
	res.ByField = []types.FieldScore{}
	res.Worse = []types.WorseCell{}
	for _, f := range p.Fields {
		res.ByField = append(res.ByField, types.FieldScore{Key: f.Key, Before: score(before, f.Key), After: score(after, f.Key)})
		for _, smp := range reviewed {
			want := textutil.ValueKey(smp.Truth[f.Key])
			if textutil.ValueKey(before[smp.ID][f.Key].Value) == want && textutil.ValueKey(after[smp.ID][f.Key].Value) != want {
				res.Worse = append(res.Worse, types.WorseCell{SampleID: smp.ID, Key: f.Key, Value: after[smp.ID][f.Key].Value, Truth: smp.Truth[f.Key]})
			}
		}
	}
	oldText := promptText(t, p, docmodel.GuideOf(ctx, s.st, owner, p, uuid.Nil), s.minConf())
	newText := promptText(t, np, docmodel.GuideOf(ctx, s.st, owner, np, uuid.Nil), s.minConf())
	res.Diff = diffLines(strings.Split(oldText, "\n"), strings.Split(newText, "\n"))
	tagDiff(res.Diff, res.Patterns, np.Shots, reviewed)
	return res, nil
}

// diffLines is the line diff of two texts by longest common subsequence.
func diffLines(a, b []string) []types.DiffLine {
	dp := make([][]int, len(a)+1)
	for i := range dp {
		dp[i] = make([]int, len(b)+1)
	}
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			if a[i] == b[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else {
				dp[i][j] = max(dp[i+1][j], dp[i][j+1])
			}
		}
	}
	var out []types.DiffLine
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] == b[j]:
			out = append(out, types.DiffLine{Op: "=", A: i + 1, B: j + 1, T: a[i]})
			i, j = i+1, j+1
		case dp[i+1][j] >= dp[i][j+1]:
			out = append(out, types.DiffLine{Op: "-", A: i + 1, T: a[i]})
			i++
		default:
			out = append(out, types.DiffLine{Op: "+", B: j + 1, T: b[j]})
			j++
		}
	}
	for ; i < len(a); i++ {
		out = append(out, types.DiffLine{Op: "-", A: i + 1, T: a[i]})
	}
	for ; j < len(b); j++ {
		out = append(out, types.DiffLine{Op: "+", B: j + 1, T: b[j]})
	}
	return out
}

// tagDiff ties each added line to its reason: the field whose rule it is,
// or the sample whose example it is part of.
func tagDiff(diff []types.DiffLine, pats []types.OptimizePattern, shots []types.PromptShot, samples []types.DocTypeSample) {
	file := map[string]uuid.UUID{}
	for _, smp := range samples {
		if slices.ContainsFunc(shots, func(sh types.PromptShot) bool { return sh.SampleID == smp.ID }) {
			file[smp.FileName] = smp.ID
		}
	}
	var shot uuid.UUID
	for i := range diff {
		l := &diff[i]
		if strings.HasPrefix(l.T, "Ví dụ ") && strings.HasSuffix(l.T, "):") {
			if k := strings.Index(l.T, "("); k > 0 {
				shot = file[strings.TrimSuffix(l.T[k+1:], "):")]
			}
		} else if strings.HasPrefix(l.T, "Nội dung giấy tờ") {
			shot = uuid.Nil // the examples end where the document to read starts
		}
		if l.Op != "+" {
			continue
		}
		for _, p := range pats {
			if strings.Contains(l.T, p.Rule) {
				l.Why = "f:" + p.Field
			}
		}
		if l.Why == "" && shot != uuid.Nil {
			l.Why = "s:" + shot.String()
		}
	}
}
