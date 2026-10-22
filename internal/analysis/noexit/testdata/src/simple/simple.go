// Package simple — эталонный пример из задания: набор полей одной структуры.
package simple

//generate:reset
type ResetableStruct struct {
	i     int
	str   string
	strP  *string
	s     []int
	m     map[string]string
	child *ResetableStruct
}
