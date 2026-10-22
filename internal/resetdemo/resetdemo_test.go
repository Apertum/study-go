package resetdemo

import "testing"

// counter считает вызовы Reset и проверяет делегирование вложенному объекту.
type counter struct{ resets int }

func (c *counter) Reset() { c.resets++ }

// silentReset — значение с Reset указательного получателя: у самого значения метода нет.
type silentReset struct{ Value int }

func (s *silentReset) Reset() { s.Value = 0 }

// plain — значение без метода Reset: генератор обязан оставить его в покое.
type plain struct{ Value int }

func filled() *ResetableStruct {
	shared := new(int)
	*shared = 7
	text := "не пусто"

	return &ResetableStruct{
		i:     42,
		str:   "строка",
		strP:  &text,
		s:     []int{1, 2, 3},
		m:     map[string]string{"ключ": "значение"},
		child: &ResetableStruct{i: 1, str: "дочерняя"},

		strings: []string{"a", "b"},
		pp:      &shared,
		items:   []*ResetableStruct{{i: 100}},
		byID:    map[string]*ResetableStruct{"id": {i: 200}},
		payload: &counter{},
	}
}

func TestResetReferenceExample(t *testing.T) {
	object := filled()
	sliceCapacity := cap(object.s)
	stringAddress := object.strP
	target := object.pp

	object.Reset()

	if object.i != 0 || object.str != "" {
		t.Errorf("скаляры не обнулены: i=%d str=%q", object.i, object.str)
	}
	if object.strP != stringAddress || *object.strP != "" {
		t.Errorf("значение по указателю сброшено неверно: адрес %p, значение %q", object.strP, *object.strP)
	}
	if len(object.s) != 0 || cap(object.s) != sliceCapacity {
		t.Errorf("слайс не обрезан: len=%d cap=%d, хотим len=0 cap=%d", len(object.s), cap(object.s), sliceCapacity)
	}
	if object.m == nil || len(object.m) != 0 {
		t.Errorf("map не очищена: %#v", object.m)
	}
	object.m["новый"] = "значение"
	if len(object.m) != 1 {
		t.Errorf("в очищенную map нельзя записывать: %#v", object.m)
	}
	if object.child == nil || object.child.i != 0 || object.child.str != "" {
		t.Errorf("вложенный объект не сброшен: %#v", object.child)
	}
	if len(object.strings) != 0 {
		t.Errorf("слайс строк не обрезан: %#v", object.strings)
	}
	if object.pp != target || **object.pp != 0 {
		t.Errorf("значение по двойному указателю не обнулено: %p %v", object.pp, **object.pp)
	}
	if payload, ok := object.payload.(*counter); !ok || payload.resets != 1 {
		t.Errorf("сброс интерфейсного поля не дошёл до значения: %#v", object.payload)
	}
}

func TestResetKeepsContainerElements(t *testing.T) {
	first := &ResetableStruct{i: 100}
	second := &ResetableStruct{i: 200}

	object := &ResetableStruct{items: []*ResetableStruct{first}, byID: map[string]*ResetableStruct{"id": second}}
	object.Reset()

	if len(object.items) != 0 || len(object.byID) != 0 {
		t.Fatalf("контейнеры не очищены: items=%d byID=%d", len(object.items), len(object.byID))
	}
	if first.i != 100 || second.i != 200 {
		t.Errorf("элементы контейнеров сброшены: %d и %d", first.i, second.i)
	}
}

func TestResetKeepsNilPointers(t *testing.T) {
	tests := []struct {
		object *ResetableStruct
		name   string
	}{
		{name: "нулевой объект", object: &ResetableStruct{}},
		{name: "указатель на nil указатель", object: &ResetableStruct{pp: new(*int)}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			test.object.Reset()

			if test.object.strP != nil || test.object.s != nil || test.object.m != nil {
				t.Errorf("nil-поля изменились: %#v", test.object)
			}
		})
	}
}

func TestResetNilReceiver(t *testing.T) {
	var object *ResetableStruct
	object.Reset()
}

func TestResetPayloadVariants(t *testing.T) {
	tests := []struct {
		payload  any
		name     string
		wantSame bool
		wantZero bool
	}{
		{name: "значение с Reset указателя", payload: &silentReset{Value: 5}, wantZero: true},
		{name: "значение без Reset", payload: plain{Value: 5}, wantSame: true},
		{name: "nil", payload: nil},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			object := &ResetableStruct{payload: test.payload}
			object.Reset()

			switch {
			case test.wantZero:
				value, ok := object.payload.(*silentReset)
				if !ok || value.Value != 0 {
					t.Errorf("сброс не дошёл до значения: %#v", object.payload)
				}
			case test.wantSame:
				value, ok := object.payload.(plain)
				if !ok || value.Value != 5 {
					t.Errorf("значение без Reset изменилось: %#v", object.payload)
				}
			case object.payload != test.payload:
				t.Errorf("поле заменено: %#v, хотим %#v", object.payload, test.payload)
			}
		})
	}
}

func TestResetIsIdempotent(t *testing.T) {
	object := filled()

	object.Reset()
	second := *object
	object.Reset()

	if object.i != second.i || len(object.s) != len(second.s) || len(object.m) != len(second.m) {
		t.Errorf("повторный сброс изменил объект: %#v после %#v", object, second)
	}
	if payload, ok := object.payload.(*counter); ok && payload.resets != 2 {
		t.Errorf("сброс вложенного объекта вызван %d раза, хотим 2", payload.resets)
	}
}
