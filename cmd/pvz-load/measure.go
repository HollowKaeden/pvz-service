package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

type cliReport struct {
	Total     int     `json:"total"`
	OK        int     `json:"ok"`
	Failed    int     `json:"failed"`
	ElapsedMS float64 `json:"elapsed_ms"`
	RPS       float64 `json:"rps"`
	Latency   struct {
		Min float64 `json:"min"`
		P50 float64 `json:"p50"`
		P95 float64 `json:"p95"`
		P99 float64 `json:"p99"`
		Max float64 `json:"max"`
	} `json:"latency_ms"`
	ByStatus map[string]int `json:"by_status"`
	ByError  map[string]int `json:"by_error"`
}
type caseReport struct {
	Name         string      `json:"name"`
	Total        int         `json:"total"`
	OK           int         `json:"ok"`
	Failed       int         `json:"failed"`
	ElapsedMS    float64     `json:"elapsed_ms"`
	RPS          float64     `json:"rps"`
	MaxStreamP99 float64     `json:"max_stream_p99_ms"`
	Passed       bool        `json:"passed"`
	Streams      []cliReport `json:"streams"`
}
type suiteReport struct {
	StartedAt         time.Time    `json:"started_at"`
	BaseURL           string       `json:"base_url"`
	BillingURL        string       `json:"billing_url"`
	Label             string       `json:"label"`
	Generator         string       `json:"go"`
	OS                string       `json:"os"`
	Arch              string       `json:"arch"`
	LogicalCPUs       int          `json:"logical_cpus"`
	TargetRPS         int          `json:"target_rps"`
	DurationSeconds   float64      `json:"duration_seconds"`
	ReadWarmupSeconds float64      `json:"read_warmup_seconds"`
	Workers           int          `json:"workers"`
	Passed            bool         `json:"passed"`
	Cases             []caseReport `json:"cases"`
}
type loadJob struct {
	url, token, body        string
	expectedStatus, workers int
	rate                    int
}

func invoke(ctx context.Context, c config, job loadJob) (cliReport, error) {
	args := []string{"-url", job.url, "-rps", strconv.Itoa(job.rate), "-d", c.duration.String(), "-c", strconv.Itoa(job.workers), "-timeout", "3s", "-json", "-H", "Authorization: Bearer " + job.token}
	if job.body != "" {
		args = append(args, "-method", http.MethodPost, "-H", "Content-Type: application/json", "-body", "@"+job.body)
	}
	// Бинарник и аргументы задаёт разработчик через CLI, shell не используется.
	cmd := exec.CommandContext(ctx, c.binary, args...) //nolint:gosec // G204: запуск явно указанного локального CLI — назначение инструмента.
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return cliReport{}, fmt.Errorf("http-loadgen завершился: %w (stderr: %s)", err, stderr.String())
	}
	var r cliReport
	if err := json.Unmarshal(stdout.Bytes(), &r); err != nil {
		return r, fmt.Errorf("JSON http-loadgen: %w", err)
	}
	return r, nil
}

func measure(ctx context.Context, c config, name string, jobs []loadJob) (caseReport, error) {
	if name == "list" && c.warmup > 0 {
		warmConfig := c
		warmConfig.duration = c.warmup
		warmCtx, cancel := context.WithTimeout(ctx, c.warmup+5*time.Second)
		r, err := invoke(warmCtx, warmConfig, jobs[0])
		cancel()
		if err != nil {
			return caseReport{}, err
		}
		if r.Total == 0 || r.Failed > 0 {
			return caseReport{}, errors.New("ошибки при прогреве чтения: измерение не начато")
		}
	}
	ctx, cancel := context.WithTimeout(ctx, c.duration+10*time.Second)
	defer cancel()
	result := caseReport{Name: name, Streams: make([]cliReport, len(jobs))}
	errorsByJob := make([]error, len(jobs))
	var wg sync.WaitGroup
	started := time.Now()
	for i, job := range jobs {
		wg.Go(func() { result.Streams[i], errorsByJob[i] = invoke(ctx, c, job) })
	}
	wg.Wait()
	elapsed := time.Since(started)
	for _, err := range errorsByJob {
		if err != nil {
			return result, err
		}
	}
	result = summarize(name, result.Streams, jobs, elapsed, c.rate)
	if err := saveJSON(filepath.Join(c.out, name+".json"), result); err != nil {
		return result, err
	}
	return result, nil
}

func summarize(name string, streams []cliReport, jobs []loadJob, elapsed time.Duration, target int) caseReport {
	result := caseReport{Name: name, Streams: streams}
	if len(streams) != len(jobs) || len(jobs) == 0 || elapsed <= 0 {
		return result
	}
	result.ElapsedMS = float64(elapsed.Microseconds()) / 1000
	for i, r := range result.Streams {
		result.Total += r.Total
		result.OK += r.ByStatus[strconv.Itoa(jobs[i].expectedStatus)] //nolint:gosec // G602: Streams создан с len(jobs), запись выполняется по тем же индексам.
		result.MaxStreamP99 = max(result.MaxStreamP99, r.Latency.P99)
	}
	result.Failed = result.Total - result.OK
	result.RPS = float64(result.Total) / elapsed.Seconds()
	// Достигнутый RPS проверяется отдельно: маленький пул не должен скрывать
	// перегрузку падением фактической скорости (coordinated omission).
	result.Passed = result.Total > 0 && result.RPS >= float64(target)*0.99 && float64(result.OK)/float64(result.Total) >= 0.9999 && result.MaxStreamP99 <= 100
	return result
}

func (a apiClient) measureProducts(ctx context.Context, c config, moderator, employee string, out io.Writer) (caseReport, error) {
	temp, err := os.MkdirTemp("", "pvz-load-")
	if err != nil {
		return caseReport{}, err
	}
	defer os.RemoveAll(temp)
	jobs := make([]loadJob, c.writers)
	ids := make([]string, c.writers)
	receptions := make([]reception, c.writers)
	for i := range jobs {
		id, rec, err := a.newReception(ctx, moderator, employee)
		if err != nil {
			return caseReport{}, err
		}
		ids[i] = id
		receptions[i] = rec
		body := filepath.Join(temp, strconv.Itoa(i)+".json")
		if err := saveJSON(body, map[string]string{"pvzId": id, "type": "обувь"}); err != nil {
			return caseReport{}, err
		}
		jobs[i] = loadJob{url: a.base + "/products", token: employee, body: body, expectedStatus: http.StatusCreated, rate: c.rate / c.writers, workers: c.workers / c.writers}
		if i < c.rate%c.writers {
			jobs[i].rate++
		}
		if i < c.workers%c.writers {
			jobs[i].workers++
		}
	}
	fmt.Fprintf(out, "POST /products: %d независимых ПВЗ, общий RPS %d\n", c.writers, c.rate)
	r, err := measure(ctx, c, "products", jobs)
	if err != nil {
		return r, err
	}
	// Проверка результата не входит в измеряемый интервал: закрываем каждую
	// приёмку и ждём точное начисление за действительно созданные товары.
	for i, id := range ids {
		if r.Streams[i].ByStatus["201"] == 0 {
			continue
		}
		if err := a.call(ctx, http.MethodPost, "/pvz/"+id+"/close_last_reception", employee, nil, nil); err != nil {
			return r, err
		}
	}
	var rates []struct {
		Type   string `json:"product_type"`
		Amount int64  `json:"amount_kopecks"`
	}
	if err := a.call(ctx, http.MethodGet, "/billing/rates", "", nil, &rates); err != nil {
		return r, err
	}
	var rate int64
	for _, v := range rates {
		if v.Type == "обувь" {
			rate = v.Amount
		}
	}
	if rate <= 0 {
		return r, errors.New("нет ставки за обувь в billing")
	}
	for i, id := range ids {
		if r.Streams[i].ByStatus["201"] == 0 {
			continue
		}
		if err := a.waitPayout(ctx, id, receptions[i].ID, int64(r.Streams[i].ByStatus["201"])*rate); err != nil {
			return r, err
		}
	}
	fmt.Fprintln(out, "Число созданных товаров подтверждено начислениями billing")
	return r, nil
}

func (a apiClient) waitPayout(ctx context.Context, id, receptionID string, amount int64) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	for ctx.Err() == nil {
		var journal []struct {
			ReceptionID string `json:"reception_id"`
			Amount      int64  `json:"amount_kopecks"`
		}
		if err := a.call(ctx, http.MethodGet, "/billing/payouts/"+id, "", nil, &journal); err != nil {
			return err
		}
		if len(journal) > 0 {
			if len(journal) != 1 || journal[0].ReceptionID != receptionID || journal[0].Amount != amount {
				return errors.New("начисление не совпало с числом успешных POST /products")
			}
			return nil
		}
		t := time.NewTimer(250 * time.Millisecond)
		select {
		case <-ctx.Done():
			t.Stop()
		case <-t.C:
		}
	}
	return fmt.Errorf("начисление не пришло: %w", ctx.Err())
}
