// sum_test.go
package sum

import (
	"testing"

	"github.com/stretchr/testify/assert"
)


func TestSum(t *testing.T) {
    tests := []struct { // добавляем слайс тестов
        name   string
        values []int
        want   int
    }{
        {
            name:   "simple test #1", // описываем каждый тест:
            values: []int{1, 2},      // значения, которые будет принимать функция,
            want:   3,                // и ожидаемый результат
        },
        {
            name:   "one",
            values: []int{1},
            want:   1,
        },
        {
            name:   "with negative values",
            values: []int{-1, -2, 3},
            want:   0,
        },
        {
            name:   "with negative zero",
            values: []int{-0, 3},
            want:   3,
        },
        {
            name:   "a lot of values",
            values: []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13,
                          14, 15, 16, 17, 18, 18},
            want:   189,
        },
    }
    for _, test := range tests { // цикл по всем тестам
        t.Run(test.name, func(t *testing.T) {
            if sum := Sum(test.values...); sum != test.want {
                t.Errorf("Sum() = %d, want %d", sum, test.want)
            }
        })
    }
}

func TestUser_FullName(t *testing.T) {
    type fields struct {
        FirstName string
        LastName  string
    }
    tests := []struct {
        name   string
        fields fields
        want   string
    }{
        {
            name: "simple test",
            fields: fields{
                FirstName: "Misha",
                LastName:  "Popov",
            },
            want: "Misha Popov",
        },
        {
            name: "long name",
            fields: fields{
                FirstName: "Pablo Diego KHoze Frantsisko de Paula KHuan" +
                    " Nepomukeno Krispin Krispiano de la Santisima Trinidad Ruiz",
                LastName: "Picasso",
            },
            want: "Pablo Diego KHoze Frantsisko de Paula KHuan Nepomukeno" +
                " Krispin Krispiano de la Santisima Trinidad Ruiz Picasso",
        },
    }
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            u := User{
                FirstName: tt.fields.FirstName,
                LastName:  tt.fields.LastName,
            }
            v := u.FullName()
            // как и в предыдущем тесте сроки сравниваются с помощью функции Equal
            assert.Equal(t, tt.want, v)
        })
    }
}