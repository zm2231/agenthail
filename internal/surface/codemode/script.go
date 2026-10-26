package codemode

import (
	"reflect"
	"regexp"

	"github.com/dop251/goja/ast"
	"github.com/dop251/goja/parser"
)

type Call struct {
	Tool   string
	Fields map[string]string
}

var scriptCallPattern = regexp.MustCompile(`\btools\.([A-Za-z_$][A-Za-z0-9_$]*)\s*\(`)

// Read keeps a script the parser rejects when its text still has the tools.NAME( shape.
func Read(input string) ([]Call, bool) {
	program, err := parser.ParseFile(nil, "", "(async function(){\n"+input+"\n})", 0)
	if err != nil {
		var calls []Call
		for _, match := range scriptCallPattern.FindAllStringSubmatch(input, -1) {
			calls = append(calls, Call{Tool: match[1]})
		}
		return calls, len(calls) > 0
	}
	var calls []Call
	walk(reflect.ValueOf(program), &calls)
	return calls, len(calls) > 0
}

func walk(value reflect.Value, calls *[]Call) {
	switch value.Kind() {
	case reflect.Interface, reflect.Pointer:
		if value.IsNil() {
			return
		}
		if call, ok := value.Interface().(*ast.CallExpression); ok && value.Kind() == reflect.Pointer {
			if tool, ok := toolName(call.Callee); ok {
				*calls = append(*calls, Call{Tool: tool, Fields: stringFields(call.ArgumentList)})
			}
		}
		walk(value.Elem(), calls)
	case reflect.Struct:
		for index := range value.NumField() {
			if field := value.Field(index); field.CanInterface() {
				walk(field, calls)
			}
		}
	case reflect.Slice:
		for index := range value.Len() {
			walk(value.Index(index), calls)
		}
	}
}

func toolName(callee ast.Expression) (string, bool) {
	dot, ok := callee.(*ast.DotExpression)
	if !ok {
		return "", false
	}
	object, ok := dot.Left.(*ast.Identifier)
	if !ok || object.Name != "tools" {
		return "", false
	}
	return dot.Identifier.Name.String(), true
}

func stringFields(arguments []ast.Expression) map[string]string {
	if len(arguments) == 0 {
		return nil
	}
	object, ok := arguments[0].(*ast.ObjectLiteral)
	if !ok {
		return nil
	}
	fields := map[string]string{}
	for _, property := range object.Value {
		keyed, ok := property.(*ast.PropertyKeyed)
		if !ok {
			continue
		}
		key, ok := literalString(keyed.Key)
		if !ok {
			continue
		}
		if value, ok := literalString(keyed.Value); ok && value != "" {
			fields[key] = value
		}
	}
	return fields
}

func literalString(expression ast.Expression) (string, bool) {
	switch value := expression.(type) {
	case *ast.StringLiteral:
		return value.Value.String(), true
	case *ast.TemplateLiteral:
		if len(value.Expressions) != 0 || len(value.Elements) != 1 {
			return "", false
		}
		return value.Elements[0].Parsed.String(), true
	}
	return "", false
}
