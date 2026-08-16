# stady-go

Вот что нужно сделать:

1. Проверьте, куда установился mockgen
   powershell
# Проверьте GOPATH
go env GOPATH

# Обычно это C:\Users\Ваше_имя\go или E:\GO (если вы меняли)
# Бинарник должен быть в: %GOPATH%\bin\mockgen.exe
2. Добавьте GOPATH/bin в PATH
   Вариант А: Временно (для текущей сессии)
   *powershell*  <br>
   $env:Path += ";$env:GOPATH\bin"


<pre>
PS E:\GO\projects\stady-go> $env:Path += ";$env:GOPATH\bin"
PS E:\GO\projects\stady-go> mockgen -source internal/store/store.go -destination internal/store/mock/store.go
E:\GO\projects\stady-go\migrations\000001_create_movies_table.up.sql
E:\GO\projects\stady-go\migrations\000001_create_movies_table.down.sql
PS E:\GO\projects\stady-go>
</pre>

#### mockgen
mockgen -source internal/store/store.go -destination internal/store/mock/store.go 


