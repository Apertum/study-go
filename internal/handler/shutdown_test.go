package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// lifecycleMarker — метка контекста процесса: по ней тест отличает его от контекста запроса.
type lifecycleMarker struct{}

const markerValue = "lifecycle"

// blockingDeleter — исполнитель мягкого удаления, который отдаёт полученный контекст и не
// возвращается, пока не откроют шлюз либо не отменят контекст процесса.
type blockingDeleter struct {
	entered  chan context.Context
	release  chan struct{}
	finished chan error
}

func newBlockingDeleter() *blockingDeleter {
	return &blockingDeleter{
		entered:  make(chan context.Context, 1),
		release:  make(chan struct{}),
		finished: make(chan error, 1),
	}
}

func (d *blockingDeleter) DeleteUrls(ctx context.Context, _ int, _ []string) error {
	d.entered <- ctx

	select {
	case <-d.release:
	case <-ctx.Done():
	}

	err := ctx.Err()
	d.finished <- err

	return err
}

// failingDeleter возвращается сразу с ошибкой — так же, как удаление в файловом режиме.
type failingDeleter struct {
	calls chan struct{}
}

func (d *failingDeleter) DeleteUrls(context.Context, int, []string) error {
	d.calls <- struct{}{}

	return errors.New("мягкое удаление недоступно в файловом режиме хранения")
}

// closedGroup сообщает, когда группа фоновых задач погасла.
func closedGroup(wg *sync.WaitGroup) <-chan struct{} {
	done := make(chan struct{})

	go func() {
		defer close(done)
		wg.Wait()
	}()

	return done
}

// deleteURLsRequest — DELETE /api/user/urls с авторизованным пользователем в контексте:
// AuthMiddleware в этих тестах не участвует, он проверен своими тестами.
func deleteURLsRequest() *http.Request {
	req := httptest.NewRequest(http.MethodDelete, "/api/user/urls", strings.NewReader(`["http://short.ru/abc"]`))
	req.Header.Set("Content-Type", "application/json")

	ctx := context.WithValue(req.Context(), contextKey{}, 7)

	return req.WithContext(ctx)
}

// TestDeleteURLsAnswersBeforeDeleteFinishes проверяет семантику 202: ответ уходит до того,
// как удаление завершено, и группа гаснет только после его возврата. Вторая проверка —
// регрессия прежнего `r.Context()`: контекст процесса сразу после ответа клиенту отменён
// быть не может, иначе запрос на удаление умирал бы вместе с ответом.
func TestDeleteURLsAnswersBeforeDeleteFinishes(t *testing.T) {
	store := newBlockingDeleter()
	var wg sync.WaitGroup

	lifecycle, cancelLifecycle := context.WithCancel(context.WithValue(context.Background(), lifecycleMarker{}, markerValue))
	defer cancelLifecycle()

	rec := httptest.NewRecorder()
	DeleteURLs(store, lifecycle, &wg).ServeHTTP(rec, deleteURLsRequest())

	if rec.Code != http.StatusAccepted {
		t.Fatalf("код ответа = %d, ожидалось %d", rec.Code, http.StatusAccepted)
	}

	deleteCtx := <-store.entered

	if err := deleteCtx.Err(); err != nil {
		t.Fatalf("контекст удаления отменён сразу после ответа клиенту: %v", err)
	}

	if deleteCtx.Value(lifecycleMarker{}) != markerValue {
		t.Fatal("удаление получило контекст запроса вместо контекста процесса")
	}

	closed := closedGroup(&wg)

	select {
	case <-closed:
		t.Fatal("группа погасла до возврата удаления")
	default:
	}

	close(store.release)

	select {
	case err := <-store.finished:
		if err != nil {
			t.Fatalf("удаление вернулось с ошибкой: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("удаление не вернулось после открытия шлюза")
	}

	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("группа фоновых задач не погасла после возврата удаления")
	}
}

// TestDeleteURLsStopsWithLifecycleContext: отмена контекста процесса ограничивает фоновое
// удаление тем же периодом, что и остальные фазы, и не оставляет группу незакрытой.
func TestDeleteURLsStopsWithLifecycleContext(t *testing.T) {
	store := newBlockingDeleter()
	var wg sync.WaitGroup

	lifecycle, cancelLifecycle := context.WithCancel(context.Background())

	rec := httptest.NewRecorder()
	DeleteURLs(store, lifecycle, &wg).ServeHTTP(rec, deleteURLsRequest())

	<-store.entered
	closed := closedGroup(&wg)

	cancelLifecycle()

	select {
	case err := <-store.finished:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("ошибка удаления = %v, ожидалось %v", err, context.Canceled)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("отмена контекста процесса не прервала удаление")
	}

	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("отмена оставила группу фоновых задач незакрытой")
	}
}

// TestDeleteURLsAnswersDespiteDeleteError: ошибка удаления остаётся в журнале, а клиент
// уже получил 202 — код ответа от неё не зависит.
func TestDeleteURLsAnswersDespiteDeleteError(t *testing.T) {
	store := &failingDeleter{calls: make(chan struct{}, 1)}
	var wg sync.WaitGroup

	lifecycle, cancelLifecycle := context.WithCancel(context.WithValue(context.Background(), lifecycleMarker{}, markerValue))
	defer cancelLifecycle()

	rec := httptest.NewRecorder()
	DeleteURLs(store, lifecycle, &wg).ServeHTTP(rec, deleteURLsRequest())

	if rec.Code != http.StatusAccepted {
		t.Fatalf("код ответа = %d, ожидалось %d", rec.Code, http.StatusAccepted)
	}

	select {
	case <-store.calls:
	case <-time.After(5 * time.Second):
		t.Fatal("удаление не запущено")
	}

	select {
	case <-closedGroup(&wg):
	case <-time.After(5 * time.Second):
		t.Fatal("ошибочное удаление не снялось со счётчика остановки")
	}
}

// TestDeleteURLsWithoutUserDoesNotStartDelete: без пользователя в контексте удаление не
// запускается — счётчик остановки не должен расти неснятой единицей.
func TestDeleteURLsWithoutUserDoesNotStartDelete(t *testing.T) {
	store := &failingDeleter{calls: make(chan struct{}, 1)}
	var wg sync.WaitGroup

	lifecycle := t.Context()

	req := httptest.NewRequest(http.MethodDelete, "/api/user/urls", strings.NewReader(`["http://short.ru/abc"]`))

	rec := httptest.NewRecorder()
	DeleteURLs(store, lifecycle, &wg).ServeHTTP(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)

	select {
	case <-store.calls:
		t.Fatal("удаление запущено без идентификатора пользователя")
	case <-time.After(200 * time.Millisecond):
	}

	closed := closedGroup(&wg)

	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("несостоявшееся удаление оставило счётчик остановки незакрытым")
	}
}

// TestDeleteURLsBadBodyDoesNotStartDelete: тело не JSON — удаления нет, 202 тоже нет.
func TestDeleteURLsBadBodyDoesNotStartDelete(t *testing.T) {
	store := &failingDeleter{calls: make(chan struct{}, 1)}
	var wg sync.WaitGroup

	lifecycle := t.Context()

	req := httptest.NewRequest(http.MethodDelete, "/api/user/urls", strings.NewReader("не список ссылок"))
	ctx := context.WithValue(req.Context(), contextKey{}, 7)

	rec := httptest.NewRecorder()
	DeleteURLs(store, lifecycle, &wg).ServeHTTP(rec, req.WithContext(ctx))

	assert.NotEqual(t, http.StatusAccepted, rec.Code)

	select {
	case <-store.calls:
		t.Fatal("удаление запущено с неразобранным телом запроса")
	case <-time.After(200 * time.Millisecond):
	}

	closed := closedGroup(&wg)

	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("остановка будет ждать удаление, которого не было")
	}
}
