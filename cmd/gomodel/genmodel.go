package main

import (
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"text/template"

	"github.com/cosiner/gohper/defval"
	"github.com/cosiner/gohper/errors"
	"github.com/cosiner/gohper/goutil"
	"github.com/cosiner/gohper/os2/file"
	"github.com/cosiner/gohper/os2/path2"
	"github.com/cosiner/gohper/strings2"
)

var (
	infile       string
	outfile      string
	models       string
	tmplfile     string
	copyTmpl     bool
	useCamelCase bool
)

func cliArgs() {
	flag.StringVar(&infile, "i", "", "input file")
	flag.StringVar(&outfile, "o", "", "output file")
	flag.StringVar(&models, "m", "", "models to parse, seperate by comma")
	flag.StringVar(&tmplfile, "t", "", "template file, first find in current directory, else use default file")

	// make it true to enable default CamelCase
	flag.BoolVar(&useCamelCase, "cc", false, "use CamelCase of constants")
	flag.BoolVar(&copyTmpl, "cp", false, "copy tmpl file to default path")
	flag.Parse()
}

const TmplName = "model.tmpl"

// change this if need
var defTmplPath = filepath.Join(path2.Home(), ".config", "go", TmplName)

func main() {
	cliArgs()
	if copyTmpl {
		errors.Fatal(file.Copy(defTmplPath, TmplName))
		return
	}
	errors.CondDo(infile == "", errors.Err("No input file specified."), errors.Fatal)

	models := strings2.TrimSplit(models, ",")
	tree, err := parser.ParseFile(token.NewFileSet(), infile, nil, 0)
	errors.Fatal(err)

	mv := new(modelVisitor)
	mv.addModelNeedParse(models)
	mv.walk(tree)

	if len(mv.models) == 0 {
		return
	}
	defval.String(&outfile, infile)

	if tmplfile == "" {
		tmplfile = TmplName
		if !file.IsExist(tmplfile) {
			tmplfile = defTmplPath
		}
	}
	errors.Fatal(
		file.OpenOrCreate(outfile, false, func(fd *os.File) error {
			modelFields := buildModelFields(mv)
			t, err := template.ParseFiles(tmplfile)
			if err == nil {
				err = t.Execute(fd, modelFields)
			}
			return err
		}),
	)
}

type StructName struct {
	Name           string // struct's normal name
	Self           string
	UnexportedName string
	UpperName      string // upper case name
	TableName      string
}

type FieldName struct {
	Name       string // field's normal name
	ColumnName string // field's column name, in snake_case
	ConstName  string // field's const name is in STRUCTNAME_FIELDNAME case
}

func NewFieldName(model *StructName, field string) *FieldName {
	f := &FieldName{
		Name: field,
	}
	if useCamelCase {
		f.ConstName = model.Name + field
	} else {
		f.ConstName = model.UpperName + "_" + strings.ToUpper(field)
	}
	return f
}

func NewStructName(name, table string) *StructName {
	s := &StructName{
		Name:           name,
		Self:           strings2.ToLowerAbridge(name),
		UnexportedName: goutil.ToUnexported(name),
		UpperName:      strings.ToUpper(name),
	}
	if table != "" {
		s.TableName = table
	} else {
		s.TableName = strings.ToLower(strings2.ToCamel(name))
	}
	return s
}

// buildModelFields build model map from parse result
func buildModelFields(mv *modelVisitor) map[*StructName][]*FieldName {
	names := make(map[*StructName][]*FieldName, len(models))
	for model, fields := range mv.models {
		modelStruct := NewStructName(model, mv.tables[model])
		for _, name := range fields {
			names[modelStruct] = append(names[modelStruct], NewFieldName(modelStruct, name))
		}
	}
	return names
}

type modelVisitor struct {
	models      map[string][]string
	tables      map[string]string
	modelsParse map[string]bool
}

func (mv *modelVisitor) addModelNeedParse(models []string) {
	mv.modelsParse = make(map[string]bool)
	for _, m := range models {
		if m != "" {
			mv.modelsParse[m] = true
		}
	}
}

// add add an model and it's field to parse result
func (mv *modelVisitor) add(model, table, field string) {
	if mv.models == nil {
		mv.models = make(map[string][]string, 10)
		mv.tables = make(map[string]string)
	}
	if table != "" {
		mv.tables[model] = table
	}
	mv.models[model] = append(mv.models[model], field)
}

// needParse check whether a model should be parsed
// unexporeted model don't parse
// if visitor's model list is not empty, only parse model exist in list
// otherwise parse all
func (mv *modelVisitor) needParse(model string) bool {
	return goutil.IsExported(model) && (len(mv.modelsParse) == 0 || mv.modelsParse[model])
}

// walk parse ast tree to find exported struct and it's fields
func (mv *modelVisitor) walk(tree *ast.File) {
	for _, decl := range tree.Decls { // Top Declare
		if decl, is := decl.(*ast.GenDecl); is { // General Declare
			if decl.Tok == token.TYPE { // Type Keyword
				for _, spec := range decl.Specs {
					spec, _ := spec.(*ast.TypeSpec)
					if t, is := spec.Type.(*ast.StructType); is { // type struct
						model := spec.Name.Name // model name
						needParse := mv.needParse(model)
						fmt.Println(model, needParse)
						if !needParse {
							continue
						}
						for _, f := range t.Fields.List { // model field
							var table string
							var tag reflect.StructTag
							if f.Tag != nil {
								tagValue := f.Tag.Value
								tag = reflect.StructTag(tagValue[1 : len(tagValue)-1]) // trim first '`' and last '`'
								if table = tag.Get("table"); table == "-" {
									break
								}
							}
							if f.Tag == nil || tag.Get("column") != "-" {
								for _, ident := range f.Names {
									if ident.IsExported() {
										mv.add(model, table, ident.Name)
									}
								}
							}
						}
					}
				}
			}
		}
	}
}
