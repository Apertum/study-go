package resetgen

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"strings"

	"golang.org/x/tools/go/packages"
)

// target — помеченная директивой структура вместе с её типом из таблицы типов.
type target struct {
	named *types.Named
	strct *ast.StructType
	name  string
}

// scan находит в файле структуры с директивой над объявлением — в порядке объявления.
// Директива над не-структурой и тип с собственным методом Reset только предупреждаются:
// остальные цели файла всё равно должны быть обработаны.
func scan(pkg *packages.Package, file *ast.File, warnings *[]Warning) []target {
	var targets []target

	for _, decl := range file.Decls {
		group, ok := decl.(*ast.GenDecl)
		if !ok || group.Tok != token.TYPE {
			continue
		}

		if len(group.Specs) > 1 && marked(group.Doc) {
			warn(warnings, pkg.Fset, group.Pos(),
				"директива %s над блоком объявлений: ни один тип не помечен, поставьте директиву над конкретным объявлением",
				Directive)
		}

		for _, spec := range group.Specs {
			typeSpec, ok := spec.(*ast.TypeSpec)
			if !ok || !marked(markDocs(group, typeSpec)...) {
				continue
			}

			strct, ok := typeSpec.Type.(*ast.StructType)
			if !ok {
				warn(warnings, pkg.Fset, typeSpec.Pos(),
					"директива %s над типом %s: метод генерируется только для структур", Directive, typeSpec.Name.Name)
				continue
			}
			// Получатель вида (g *Generic[T]) в объявлении метода Go допускает только полный список
			// параметров типа, поэтому для параметризованной структуры метод не генерируется.
			if typeSpec.TypeParams != nil {
				warn(warnings, pkg.Fset, typeSpec.Pos(),
					"тип %s параметризован: генерация пропущена, метод Reset для generic-структур не создаётся",
					typeSpec.Name.Name)
				continue
			}

			defined, ok := pkg.TypesInfo.Defs[typeSpec.Name].(*types.TypeName)
			if !ok {
				continue
			}
			named, ok := defined.Type().(*types.Named)
			if !ok {
				continue
			}
			if hasOwnReset(pkg.Fset, named) {
				warn(warnings, pkg.Fset, typeSpec.Pos(),
					"тип %s уже объявляет метод %s: генерация пропущена, чтобы не повторять объявление метода",
					typeSpec.Name.Name, resetMethod)
				continue
			}

			targets = append(targets, target{name: typeSpec.Name.Name, named: named, strct: strct})
		}
	}

	return targets
}

// markDocs возвращает комментарии, наличие директивы в которых помечает тип: собственный комментарий
// объявления, а для одиночного объявления — и комментарий группы. Комментарий группы с несколькими
// объявлениями целью не считает: одна директива не должна помечать все типы блока.
func markDocs(group *ast.GenDecl, typeSpec *ast.TypeSpec) []*ast.CommentGroup {
	if len(group.Specs) == 1 {
		return []*ast.CommentGroup{typeSpec.Doc, group.Doc}
	}
	return []*ast.CommentGroup{typeSpec.Doc}
}

// marked сообщает, что один из комментариев группы содержит директиву. Пробел после // и регистр
// не значимы: //generate:reset и // Generate:Reset помечают одинаково.
func marked(groups ...*ast.CommentGroup) bool {
	for _, group := range groups {
		if group == nil {
			continue
		}
		for _, comment := range group.List {
			text := strings.TrimSpace(strings.TrimPrefix(comment.Text, "//"))
			if strings.EqualFold(text, Directive) {
				return true
			}
		}
	}
	return false
}

// hasOwnReset сообщает, что для самого типа объявлен метод Reset — с получателем T или *T.
// Метод, полученный от встроенного типа (promotion), собственным не считается: внешний тип всё
// равно должен получить свой Reset, иначе встроенный тип сбросил бы только свои поля. Метод из
// сгенерированного файла тоже не считается собственным — иначе второй запуск генератора не нашёл
// бы цель и удалил собственный выходной файл как осиротевший.
func hasOwnReset(fset *token.FileSet, named *types.Named) bool {
	for _, receiver := range []types.Type{named, types.NewPointer(named)} {
		for selection := range types.NewMethodSet(receiver).Methods() {
			fn, ok := selection.Obj().(*types.Func)
			if !ok || fn.Name() != resetMethod {
				continue
			}
			signature, ok := fn.Type().(*types.Signature)
			if !ok || signature.Recv() == nil {
				continue
			}
			if namedOf(signature.Recv().Type()) != named {
				continue
			}
			if !isGeneratedFile(fset, fn.Pos()) {
				return true
			}
		}
	}
	return false
}

// isGeneratedFile сообщает, что объявление стоит в файле, который ведёт этот генератор.
func isGeneratedFile(fset *token.FileSet, pos token.Pos) bool {
	return strings.HasSuffix(fset.Position(pos).Filename, GeneratedSuffix)
}

// namedOf возвращает именованный тип без указателя и псевдонима.
func namedOf(typ types.Type) *types.Named {
	typ = types.Unalias(typ)
	if pointer, ok := typ.(*types.Pointer); ok {
		typ = types.Unalias(pointer.Elem())
	}
	named, _ := typ.(*types.Named)
	return named
}

func warn(warnings *[]Warning, fset *token.FileSet, pos token.Pos, format string, args ...any) {
	*warnings = append(*warnings, Warning{
		Position: fset.Position(pos),
		Message:  fmt.Sprintf(format, args...),
	})
}
