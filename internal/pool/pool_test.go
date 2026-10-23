package pool

import (
	"fmt"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"

	"study-go.ru/cho/eto/internal/resetdemo"
)

// nested — вложенный объект: его сброс внешний пул обязан получить через собственный Reset.
type nested struct {
	count int
}

func (n *nested) Reset() {
	n.count = 0
}

// item — объект с полями всех видов, на которых проверяется сброс перед возвратом в пул.
// Поля с указателями в начале структуры, скаляр в конце: так меньше префикс, который
// обходит сборщик мусора (это же требует fieldalignment).
type item struct {
	child *nested
	byKey map[string]int
	text  string
	list  []int
	count int
}

func (i *item) Reset() {
	i.list = i.list[:0]
	i.text = ""

	if i.child != nil {
		i.child.Reset()
	}

	clear(i.byKey)
	i.count = 0
}

// valueItem — Reset на получателе-значении: пул для него создаётся, хотя сам сброс и меняет
// только копию аргумента. Именно поэтому рабочий случай проекта — указатель (см. документацию
// пакета), а метод оставлен пустым: запись в v.count ничего не сбрасывает.
type valueItem struct {
	count int
}

func (valueItem) Reset() {}

// iface — интерфейсный параметр типа: нового объекта для него не существует.
type iface interface{ Reset() }

const (
	// reuseAttempts — число попыток дождаться переиспользования: sync.Pool вправе отдать
	// новый экземпляр, поэтому одна пара Put/Get для проверки не показательна.
	reuseAttempts = 64

	listCapacity  = 8
	itemsPerBatch = 10
	raceWorkers   = 8
	raceCycles    = 1000

	// putsPerObject — сколько раз один и тот же экземпляр возвращают в пул в сценарии
	// двойного возврата: пул не контролирует право собственности.
	putsPerObject = 2
)

func filledItem() *item {
	list := make([]int, 0, listCapacity)

	return &item{
		list:  append(list, 1, 2, 3),
		text:  "значение",
		child: &nested{count: 5},
		byKey: map[string]int{"ключ": 1},
		count: 42,
	}
}

// dirtyReason описывает несохранённое нулю поле объекта; пустая строка означает, что он чист.
func dirtyReason(object *item) string {
	switch {
	case object == nil:
		return "получен nil вместо объекта"
	case object.count != 0 || object.text != "":
		return fmt.Sprintf("скалярные поля не сброшены: count=%d text=%q", object.count, object.text)
	case len(object.list) != 0:
		return fmt.Sprintf("слайс-поле не очищено: %#v", object.list)
	case len(object.byKey) != 0:
		return fmt.Sprintf("карта-поле не очищено: %#v", object.byKey)
	case object.child != nil && object.child.count != 0:
		return fmt.Sprintf("вложенный объект не сброшен: count=%d", object.child.count)
	default:
		return ""
	}
}

// assertClean проверяет, что все поля объекта сброшены в нулевые значения.
func assertClean(t *testing.T, object *item) {
	t.Helper()

	if object == nil {
		t.Fatal("получен nil вместо объекта")
	}

	if reason := dirtyReason(object); reason != "" {
		t.Error(reason)
	}
}

// reuseItem возвращает объект обратно в пул и ждёт, что sync.Pool отдаст ровно этот
// экземпляр. Возврат повторяется на каждой попытке: слот пула привязан к исполнителю,
// и после миграции горутины одна передача объекта ничего не гарантирует. Без ожидания
// того же адреса проверка бессмысленна: вновь созданный объект чист по определению.
func reuseItem(t *testing.T, created *Pool[*item], want *item) *item {
	t.Helper()

	for range reuseAttempts {
		created.Put(want)

		if candidate := created.Get(); candidate == want {
			assertClean(t, candidate)

			return candidate
		}
	}

	t.Fatalf("пул не вернул тот же экземпляр за %d попыток", reuseAttempts)

	return want
}

func TestNewReturnsIndependentPools(t *testing.T) {
	t.Parallel()

	first := New[*item]()
	if first == nil {
		t.Fatal("New вернул nil")
	}

	second := New[*item]()
	held := filledItem()
	first.Put(held)

	if second.Get() == held {
		t.Error("второй пул отдал объект первого")
	}
}

func TestGetFromEmptyPoolCreatesZeroedObject(t *testing.T) {
	t.Parallel()

	created := New[*item]()
	object := created.Get()
	assertClean(t, object)

	if again := created.Get(); again == object {
		t.Error("два получения из пустого пула вернули один экземпляр")
	}
}

func TestNewAcceptsValueTypeParameter(t *testing.T) {
	t.Parallel()

	created := New[valueItem]()
	object := created.Get()

	if object.count != 0 {
		t.Errorf("нулевое значение не получено: %#v", object)
	}

	created.Put(object)

	if created.Get().count != 0 {
		t.Error("получение после возврата дало не нулевое значение")
	}
}

func TestNewPanicsForInterfaceParameter(t *testing.T) {
	t.Parallel()

	var created *Pool[iface]

	defer func() {
		message, ok := recover().(string)
		switch {
		case !ok:
			t.Fatalf("паника не строкой: %v", message)
		case !strings.Contains(message, "pool"):
			t.Errorf("сообщение без имени пакета: %q", message)
		}
	}()

	created = New[iface]()
	t.Errorf("создание пула интерфейсного типа не завершилось паникой: %#v", created)
}

func TestGetRejectsForeignStoredType(t *testing.T) {
	t.Parallel()

	created := New[*item]()
	// Хранилище sync.Pool закрыто any, поэтому чужое значение попадает в него только
	// изнутри пакета — так выглядит повреждение хранилища пула.
	created.pool.Put(&nested{})

	defer func() {
		message, ok := recover().(string)
		switch {
		case !ok:
			t.Fatalf("паника не строкой: %v", message)
		case !strings.Contains(message, "pool"):
			t.Errorf("сообщение без имени пакета: %q", message)
		}
	}()

	if object := created.Get(); object != nil {
		t.Errorf("чужое значение выдано вместо паники: %#v", object)
	}
}

func TestPoolWithGeneratedResetMethod(t *testing.T) {
	t.Parallel()

	created := New[*resetdemo.ResetableStruct]()
	object := created.Get()

	if object == nil {
		t.Fatal("получен nil из пула генерируемого типа")
	}

	created.Put(object)

	var nilObject *resetdemo.ResetableStruct
	created.Put(nilObject)

	if created.Get() == nil {
		t.Error("после возврата nil получение вернуло nil")
	}
}

func TestPutDropsNilArgument(t *testing.T) {
	t.Parallel()

	// Порядок полей под fieldalignment: функция-заполнитель идёт перед именем подтеста.
	tests := []struct {
		fill func(created *Pool[*item])
		name string
	}{
		{
			name: "только nil",
			fill: func(created *Pool[*item]) { created.Put(nil) },
		},
		{
			name: "корректные объекты и nil",
			fill: func(created *Pool[*item]) {
				created.Put(filledItem())
				created.Put(nil)
				created.Put(filledItem())
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			created := New[*item]()
			test.fill(created)
			assertClean(t, created.Get())
		})
	}
}

func TestPutResetsStateItself(t *testing.T) {
	t.Parallel()

	created := New[*item]()

	reuseItem(t, created, filledItem())
}

func TestPutResetsNestedObject(t *testing.T) {
	t.Parallel()

	created := New[*item]()
	object := filledItem()
	child := object.child

	got := reuseItem(t, created, object)

	if got.child != child {
		t.Fatalf("вложенный объект заменён: %p вместо %p", got.child, child)
	}

	if child.count != 0 {
		t.Errorf("вложенный объект не сброшен: count=%d", child.count)
	}
}

func TestPutKeepsEveryObject(t *testing.T) {
	t.Parallel()

	created := New[*item]()
	addresses := make(map[*item]bool, itemsPerBatch)

	for range itemsPerBatch {
		object := filledItem()
		addresses[object] = true
		created.Put(object)
	}

	reused := 0

	for range itemsPerBatch {
		object := created.Get()

		if reason := dirtyReason(object); reason != "" {
			t.Fatalf("%s", reason)
		}

		if addresses[object] {
			reused++
		}
	}

	// Переиспользование здесь не проверяется: сборка мусора из параллельного теста вправе
	// очистить пул, а гарантию возврата экземпляра спецификация пулу не даёт.
	t.Logf("переиспользовано %d объектов из %d", reused, itemsPerBatch)
}

func TestSameObjectPutTwiceIsAllowed(t *testing.T) {
	t.Parallel()

	created := New[*item]()
	object := filledItem()

	for range putsPerObject {
		created.Put(object)
	}

	for range putsPerObject {
		assertClean(t, created.Get())
	}
}

func TestReusedObjectKeepsAllocations(t *testing.T) {
	t.Parallel()

	created := New[*item]()
	object := filledItem()
	object.list = append(object.list, 4, 5)

	keysAddress := reflect.ValueOf(object.byKey).Pointer()
	got := reuseItem(t, created, object)

	if len(got.list) != 0 {
		t.Errorf("слайс не обрезан: len=%d", len(got.list))
	}

	if cap(got.list) != listCapacity {
		t.Errorf("ёмкость слайса потеряна: cap=%d, хотим %d", cap(got.list), listCapacity)
	}

	if got.byKey == nil || reflect.ValueOf(got.byKey).Pointer() != keysAddress {
		t.Fatalf("карта заменена новой: %#v", got.byKey)
	}

	got.byKey["новый"] = 1

	if len(got.byKey) != 1 {
		t.Errorf("в очищенную карту нельзя записывать: %#v", got.byKey)
	}
}

func TestGetAfterGarbageCollectionIsUsable(t *testing.T) {
	t.Parallel()

	created := New[*item]()
	created.Put(filledItem())
	runtime.GC()

	assertClean(t, created.Get())
}

func TestConcurrentGetAndPut(t *testing.T) {
	t.Parallel()

	created := New[*item]()

	var group sync.WaitGroup

	for range raceWorkers {
		group.Go(func() {
			for range raceCycles {
				object := created.Get()

				// t.Fatal из отдельной горутины недоступен, поэтому только описание причины.
				if reason := dirtyReason(object); reason != "" {
					t.Error(reason)

					return
				}

				object.count = 1
				object.text = "занято"
				created.Put(object)
			}
		})
	}

	group.Wait()
}
