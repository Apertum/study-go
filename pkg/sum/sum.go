package sum

// User — пользователь в системе.
type User struct {
	FirstName string
	LastName  string
}

// Sum возвращает сумму элементов.
func Sum(values ...int) int {
	var sum int
	for _, v := range values {
		sum += v
	}
	return sum
}

// FullName возвращает имя и фамилию пользователя.
func (u User) FullName() string {
	return u.FirstName + " " + u.LastName
}
