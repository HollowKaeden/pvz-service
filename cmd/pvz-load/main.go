// pvz-load готовит данные через публичный API и запускает CLI http-loadgen.
// Измеритель не импортирует код сервера и не обращается к его БД.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
)

type config struct {
	base, billing, binary, out, profile, label string
	rate                                       int
	duration                                   time.Duration
	warmup                                     time.Duration
	workers, writers                           int
	check                                      bool
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := run(ctx, os.Args[1:], os.Stdout)
	stop()
	if errors.Is(err, flag.ErrHelp) {
		return
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func parse(args []string) (config, error) {
	var c config
	f := flag.NewFlagSet("pvz-load", flag.ContinueOnError)
	f.StringVar(&c.base, "url", "http://127.0.0.1:8000", "адрес PVZ API (корень)")
	f.StringVar(&c.billing, "billing-url", "", "адрес billing, если отличается от -url")
	defaultBinary := "bin/loadgen"
	if runtime.GOOS == "windows" {
		defaultBinary += ".exe"
	}
	f.StringVar(&c.binary, "loadgen-bin", defaultBinary, "собранный CLI http-loadgen")
	f.StringVar(&c.out, "out", "tmp/load", "каталог fixture и JSON-отчётов (без токенов)")
	f.StringVar(&c.profile, "profile", "all", "list, products или all")
	f.StringVar(&c.label, "label", "", "описание стенда для отчёта")
	f.IntVar(&c.rate, "rps", 1000, "общий целевой RPS каждого сценария")
	f.DurationVar(&c.duration, "duration", 30*time.Second, "длительность сценария")
	f.DurationVar(&c.warmup, "warmup", 3*time.Second, "прогрев чтения перед измерением (в отчёт не входит)")
	f.IntVar(&c.workers, "workers", 100, "общее число воркеров генератора")
	f.IntVar(&c.writers, "writers", 10, "независимые ПВЗ в сценарии добавления товаров")
	f.BoolVar(&c.check, "check", true, "ненулевой exit code при невыполнении SLI")
	if err := f.Parse(args); err != nil {
		return c, err
	}
	c.base = strings.TrimRight(c.base, "/")
	u, err := url.Parse(c.base)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return c, errors.New("-url: нужен http(s) origin без пути, query и учётных данных")
	}
	if c.billing == "" {
		c.billing = c.base
	}
	c.billing = strings.TrimRight(c.billing, "/")
	b, err := url.Parse(c.billing)
	if err != nil || b.Host == "" || (b.Scheme != "http" && b.Scheme != "https") || b.User != nil || b.Path != "" || b.RawQuery != "" || b.Fragment != "" {
		return c, errors.New("-billing-url: нужен http(s) origin")
	}
	if f.NArg() != 0 {
		return c, errors.New("лишние аргументы")
	}
	if c.profile != "list" && c.profile != "products" && c.profile != "all" {
		return c, errors.New("-profile: list, products или all")
	}
	if c.rate < 1 || c.rate > 20000 || c.duration < time.Second || c.duration > time.Minute || c.warmup < 0 || c.warmup > 30*time.Second || c.workers < 1 || c.workers > 500 || c.writers < 1 || c.writers > 50 {
		return c, errors.New("RPS: 1–20000, duration: 1s–1m, warmup: 0–30s, workers: 1–500, writers: 1–50")
	}
	if c.profile != "list" && (c.workers < c.writers || c.rate < c.writers || float64(c.rate)*c.duration.Seconds()/float64(c.writers) > 8000) {
		return c, errors.New("для products: workers и RPS >= writers, не более 8000 товаров на одну приёмку (лимит события)")
	}
	return c, nil
}

func run(ctx context.Context, args []string, out io.Writer) error {
	c, err := parse(args)
	if err != nil {
		return err
	}
	binary, err := filepath.Abs(c.binary)
	if err != nil {
		return err
	}
	c.binary = binary
	if _, err := os.Stat(binary); err != nil {
		return fmt.Errorf("соберите CLI http-loadgen (make load): %w", err)
	}
	if err := os.MkdirAll(c.out, 0o750); err != nil {
		return err
	}
	// Не оставляем успешный итог прошлого запуска, если новый упадёт на
	// подготовке данных или проверке начислений. История — отдельные -out.
	if err := os.Remove(filepath.Join(c.out, "summary.json")); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	client := apiClient{base: c.base, billing: c.billing, http: &http.Client{Timeout: 10 * time.Second}}
	var moderator, employee string
	if err := client.call(ctx, http.MethodPost, "/dummyLogin", "", map[string]string{"role": "moderator"}, &moderator); err != nil {
		return err
	}
	if err := client.call(ctx, http.MethodPost, "/dummyLogin", "", map[string]string{"role": "employee"}, &employee); err != nil {
		return err
	}
	result := suiteReport{StartedAt: time.Now().UTC(), BaseURL: c.base, Label: c.label, Generator: runtime.Version(), OS: runtime.GOOS, Arch: runtime.GOARCH, LogicalCPUs: runtime.NumCPU(), TargetRPS: c.rate, DurationSeconds: c.duration.Seconds(), Workers: c.workers}
	result.BillingURL = c.billing
	result.ReadWarmupSeconds = c.warmup.Seconds()
	if c.profile != "products" {
		fixture, err := client.readFixture(ctx, c.out, moderator, employee)
		if err != nil {
			return err
		}
		fmt.Fprintln(out, "GET /pvz: 10 ПВЗ × 1 закрытая приёмка × 50 товаров (фильтр по датам)")
		r, err := measure(ctx, c, "list", []loadJob{{url: c.base + fixture.Query, token: employee, expectedStatus: http.StatusOK, workers: c.workers, rate: c.rate}})
		if err != nil {
			return err
		}
		result.Cases = append(result.Cases, r)
	}
	if c.profile != "list" {
		r, err := client.measureProducts(ctx, c, moderator, employee, out)
		if err != nil {
			return err
		}
		result.Cases = append(result.Cases, r)
	}
	result.Passed = true
	for _, r := range result.Cases {
		if !r.Passed {
			result.Passed = false
		}
	}
	if err := saveJSON(filepath.Join(c.out, "summary.json"), result); err != nil {
		return err
	}
	for _, r := range result.Cases {
		fmt.Fprintf(out, "%s: %.2f RPS, %d/%d успешно, max p99 потоков %.2f мс, SLI=%t\n", r.Name, r.RPS, r.OK, r.Total, r.MaxStreamP99, r.Passed)
	}
	fmt.Fprintln(out, "Отчёт:", filepath.Join(c.out, "summary.json"))
	if c.check && !result.Passed {
		return errors.New("SLI не выполнены: подробности в summary.json (достигнутый RPS, коды, ошибки, p99)")
	}
	return nil
}

func saveJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o600)
}
