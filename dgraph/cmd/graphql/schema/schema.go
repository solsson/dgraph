/*
 * Copyright 2019 Dgraph Labs, Inc. and Contributors
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package schema

import (
	"fmt"
	"sort"
	"strings"

	"github.com/vektah/gqlparser/ast"
	"github.com/vektah/gqlparser/gqlerror"
	"github.com/vektah/gqlparser/parser"
	"github.com/vektah/gqlparser/validator"
)

type schRuleFunc func(schema *ast.SchemaDocument) *gqlerror.Error

type schRule struct {
	name        string
	schRuleFunc schRuleFunc
}

type scalar struct {
	name       string
	dgraphType string
}

type args struct {
	name    string
	gqlType string
	nonNull bool
}

type directive struct {
	name string
	args ast.ArgumentDefinitionList
}

const (
	inverseName   = "hasInverse"
	inverseFldArg = "field"
)

var schRules []schRule

var supportedScalars = []scalar{
	{name: "ID", dgraphType: "uid"},
	{name: "Boolean", dgraphType: "bool"},
	{name: "Int", dgraphType: "int"},
	{name: "Float", dgraphType: "float"},
	{name: "String", dgraphType: "string"},
	{name: "DateTime", dgraphType: "dateTime"}}

var supportedDirectives = []directive{
	{name: inverseName,
		args: ast.ArgumentDefinitionList{
			{Name: inverseFldArg, Type: &ast.Type{NamedType: "String", NonNull: true}}}}}

// AddScalars adds all the supported scalars in the schema.
func AddScalars(doc *ast.SchemaDocument) {
	for _, s := range supportedScalars {
		addScalar(s, doc)
	}
}

// AddDirectives add all the supported directives to schema.
func AddDirectives(doc *ast.SchemaDocument) {
	for _, d := range supportedDirectives {
		addDirective(d, []ast.DirectiveLocation{ast.LocationField}, doc)
	}
}

func addScalar(s scalar, doc *ast.SchemaDocument) {
	doc.Definitions = append(
		doc.Definitions,
		// Empty Position because it is being inserted by the engine.
		&ast.Definition{Kind: ast.Scalar, Name: s.name, Position: &ast.Position{}},
	)
}

func addDirective(d directive, locations []ast.DirectiveLocation, doc *ast.SchemaDocument) {
	doc.Directives = append(doc.Directives, &ast.DirectiveDefinition{
		Name:      d.name,
		Locations: locations,
		Arguments: d.args,
	})
}

// AddRule adds a new schema rule to the global array schRules.
func AddRule(name string, f schRuleFunc) {
	schRules = append(schRules, schRule{
		name:        name,
		schRuleFunc: f,
	})
}

// ValidateSchema validates the schema against dgraph's rules of schema.
func ValidateSchema(schema *ast.SchemaDocument) gqlerror.List {
	var errs []*gqlerror.Error

	for i := range schRules {
		if gqlErr := schRules[i].schRuleFunc(schema); gqlErr != nil {
			errs = append(errs, gqlErr)
		}
	}

	return errs
}

// GenerateCompleteSchema generates all the required query/mutation/update functions
// for all the types mentioned the the schema.
func GenerateCompleteSchema(inputSchema string) (*ast.Schema, gqlerror.List) {

	doc, gqlErr := parser.ParseSchema(&ast.Source{Input: inputSchema})
	if gqlErr != nil {
		return nil, []*gqlerror.Error{gqlErr}
	}

	if gqlErrList := ValidateSchema(doc); gqlErrList != nil {
		return nil, gqlErrList
	}

	AddScalars(doc)
	AddDirectives(doc)

	schema, gqlErr := validator.ValidateSchemaDocument(doc)
	if gqlErr != nil {
		return nil, []*gqlerror.Error{gqlErr}
	}

	extenderMap := make(map[string]*ast.Definition)

	schema.Query = &ast.Definition{
		Kind:        ast.Object,
		Description: "Query object contains all the query functions",
		Name:        "Query",
		Fields:      make([]*ast.FieldDefinition, 0),
	}

	schema.Mutation = &ast.Definition{
		Kind:        ast.Object,
		Description: "Mutation object contains all the mutation functions",
		Name:        "Mutation",
		Fields:      make([]*ast.FieldDefinition, 0),
	}

	for _, defn := range schema.Types {
		if defn.Kind == ast.Object {
			extenderMap[defn.Name+"Input"] = genInputType(schema, defn)
			extenderMap[defn.Name+"Ref"] = genRefType(defn)
			extenderMap[defn.Name+"Update"] = genUpdateType(schema, defn)
			extenderMap[defn.Name+"Filter"] = genFilterType(defn)
			extenderMap["Add"+defn.Name+"Payload"] = genAddResultType(defn)
			extenderMap["Update"+defn.Name+"Payload"] = genUpdResultType(defn)
			extenderMap["Delete"+defn.Name+"Payload"] = genDelResultType(defn)

			schema.Query.Fields = append(schema.Query.Fields, addQueryType(defn)...)
			schema.Mutation.Fields = append(schema.Mutation.Fields, addMutationType(defn)...)
		}
	}

	for name, extType := range extenderMap {
		schema.Types[name] = extType
	}

	return schema, nil
}

// AreEqualSchema checks if sch1 and sch2 are the same schema.
func AreEqualSchema(sch1, sch2 *ast.Schema) bool {
	return AreEqualQuery(sch1.Query, sch2.Query) &&
		AreEqualMutation(sch1.Mutation, sch2.Mutation) &&
		AreEqualTypes(sch1.Types, sch2.Types)
}

// AreEqualQuery checks if query blocks qry1, qry2 are same.
func AreEqualQuery(qry1, qry2 *ast.Definition) bool {
	return AreEqualFields(qry1.Fields, qry2.Fields)
}

// AreEqualMutation checks if mutation blocks mut1, mut2 are same.
func AreEqualMutation(mut1, mut2 *ast.Definition) bool {
	return AreEqualFields(mut1.Fields, mut2.Fields)
}

// AreEqualTypes checks if types typ1, typ2 are same.
func AreEqualTypes(typ1, typ2 map[string]*ast.Definition) bool {
	for name, def := range typ1 {
		val, ok := typ2[name]

		if !ok || def.Kind != val.Kind {
			return false
		}

		if !AreEqualFields(def.Fields, val.Fields) {
			return false
		}
	}

	return true
}

// AreEqualFields checks if fieldlist flds1, flds2 are same.
func AreEqualFields(flds1, flds2 ast.FieldList) bool {
	fldDict := make(map[string]*ast.FieldDefinition)

	for _, fld := range flds1 {
		fldDict[fld.Name] = fld
	}

	for _, fld := range flds2 {

		if strings.HasPrefix(fld.Name, "__") {
			continue
		}
		val, ok := fldDict[fld.Name]

		if !ok {
			return false
		}

		if genFieldString(fld) != genFieldString(val) {
			return false
		}
	}

	return true
}

func genInputType(schema *ast.Schema, defn *ast.Definition) *ast.Definition {
	return &ast.Definition{
		Kind:   ast.InputObject,
		Name:   defn.Name + "Input",
		Fields: getNonIDFields(schema, defn),
	}
}

func genRefType(defn *ast.Definition) *ast.Definition {
	return &ast.Definition{
		Kind:   ast.InputObject,
		Name:   defn.Name + "Ref",
		Fields: getIDField(defn),
	}
}

func genUpdateType(schema *ast.Schema, defn *ast.Definition) *ast.Definition {
	updDefn := &ast.Definition{
		Kind:   ast.InputObject,
		Name:   defn.Name + "Update",
		Fields: getNonIDFields(schema, defn),
	}

	for _, fld := range updDefn.Fields {
		fld.Type.NonNull = false
	}

	return updDefn
}

func genFilterType(defn *ast.Definition) *ast.Definition {
	return &ast.Definition{
		Kind:   ast.InputObject,
		Name:   defn.Name + "Filter",
		Fields: getFilterField(),
	}
}

func genAddResultType(defn *ast.Definition) *ast.Definition {
	return &ast.Definition{
		Kind: ast.Object,
		Name: "Add" + defn.Name + "Payload",
		Fields: []*ast.FieldDefinition{
			&ast.FieldDefinition{
				Name: strings.ToLower(defn.Name),
				Type: &ast.Type{
					NamedType: defn.Name,
					NonNull:   true,
				},
			},
		},
	}
}

func genUpdResultType(defn *ast.Definition) *ast.Definition {
	return &ast.Definition{
		Kind: ast.Object,
		Name: "Update" + defn.Name + "Payload",
		Fields: []*ast.FieldDefinition{
			&ast.FieldDefinition{ // Field type is same as the parent object type
				Name: strings.ToLower(defn.Name),
				Type: &ast.Type{
					NamedType: defn.Name,
					NonNull:   true,
				},
			},
		},
	}
}

func genDelResultType(defn *ast.Definition) *ast.Definition {
	return &ast.Definition{
		Kind: ast.Object,
		Name: "Delete" + defn.Name + "Payload",
		Fields: []*ast.FieldDefinition{
			&ast.FieldDefinition{
				Name: "msg",
				Type: &ast.Type{
					NamedType: "String",
					NonNull:   true,
				},
			},
		},
	}
}

func createGetFld(defn *ast.Definition) *ast.FieldDefinition {
	return &ast.FieldDefinition{
		Description: "Query " + defn.Name + " by ID",
		Name:        "get" + defn.Name,
		Type: &ast.Type{
			NamedType: defn.Name,
			NonNull:   true,
		},
		Arguments: []*ast.ArgumentDefinition{
			&ast.ArgumentDefinition{
				Name: "id",
				Type: &ast.Type{
					NamedType: idTypeFor(defn),
					NonNull:   true,
				},
			},
		},
	}
}

func createQryFld(defn *ast.Definition) *ast.FieldDefinition {
	return &ast.FieldDefinition{
		Description: "Query " + defn.Name,
		Name:        "query" + defn.Name,
		Type: &ast.Type{
			NonNull: true,
			Elem: &ast.Type{
				NamedType: defn.Name,
				NonNull:   true,
			},
		},
		Arguments: []*ast.ArgumentDefinition{
			&ast.ArgumentDefinition{
				Name: "filter",
				Type: &ast.Type{
					NamedType: defn.Name + "Filter",
					NonNull:   true,
				},
			},
		},
	}
}

func addQueryType(defn *ast.Definition) (flds []*ast.FieldDefinition) {
	flds = append(flds, createGetFld(defn))
	flds = append(flds, createQryFld(defn))

	return
}

func createAddFld(defn *ast.Definition) *ast.FieldDefinition {
	return &ast.FieldDefinition{
		Description: "Function for adding " + defn.Name,
		Name:        "add" + defn.Name,
		Type: &ast.Type{
			NamedType: "Add" + defn.Name + "Payload",
			NonNull:   true,
		},
		Arguments: []*ast.ArgumentDefinition{
			&ast.ArgumentDefinition{
				Name: "input",
				Type: &ast.Type{
					NamedType: defn.Name + "Input",
					NonNull:   true,
				},
			},
		},
	}
}

func createUpdFld(defn *ast.Definition) *ast.FieldDefinition {
	updArgs := make([]*ast.ArgumentDefinition, 0)
	updArg := &ast.ArgumentDefinition{
		Name: "id",
		Type: &ast.Type{
			NamedType: idTypeFor(defn),
			NonNull:   true,
		},
	}
	updArgs = append(updArgs, updArg)
	updArg = &ast.ArgumentDefinition{
		Name: "input",
		Type: &ast.Type{
			NamedType: defn.Name + "Update",
			NonNull:   false,
		},
	}
	updArgs = append(updArgs, updArg)

	return &ast.FieldDefinition{
		Description: "Function for updating " + defn.Name,
		Name:        "update" + defn.Name,
		Type: &ast.Type{
			NamedType: "Update" + defn.Name + "Payload",
			NonNull:   true,
		},
		Arguments: updArgs,
	}
}

func createDelFld(defn *ast.Definition) *ast.FieldDefinition {
	return &ast.FieldDefinition{
		Description: "Function for deleting " + defn.Name,
		Name:        "delete" + defn.Name,
		Type: &ast.Type{
			NamedType: "Delete" + defn.Name + "Payload",
			NonNull:   true,
		},
		Arguments: []*ast.ArgumentDefinition{
			&ast.ArgumentDefinition{
				Name: "id",
				Type: &ast.Type{
					NamedType: idTypeFor(defn),
					NonNull:   true,
				},
			},
		},
	}
}

func addMutationType(defn *ast.Definition) (flds []*ast.FieldDefinition) {
	flds = append(flds, createAddFld(defn))
	flds = append(flds, createUpdFld(defn))
	flds = append(flds, createDelFld(defn))

	return
}

func getFilterField() ast.FieldList {
	return []*ast.FieldDefinition{
		&ast.FieldDefinition{
			Name: "dgraph",
			Type: &ast.Type{
				NamedType: "String",
			},
		},
	}
}

func getNonIDFields(schema *ast.Schema, defn *ast.Definition) ast.FieldList {
	fldList := make([]*ast.FieldDefinition, 0)
	for _, fld := range defn.Fields {
		if isIDField(defn, fld) {
			continue
		}
		if schema.Types[fld.Type.Name()].Kind == ast.Object {
			newDefn := &ast.FieldDefinition{
				Name: fld.Name,
			}

			newDefn.Type = &ast.Type{}
			newDefn.Type.NonNull = fld.Type.NonNull
			if fld.Type.NamedType != "" {
				newDefn.Type.NamedType = fld.Type.Name() + "Ref"
			} else {
				newDefn.Type.Elem = &ast.Type{
					NamedType: fld.Type.Name() + "Ref",
					NonNull:   fld.Type.Elem.NonNull,
				}
			}

			fldList = append(fldList, newDefn)
		} else {
			newFld := *fld
			newFldType := *fld.Type
			newFld.Type = &newFldType
			fldList = append(fldList, &newFld)
		}
	}
	return fldList
}

func getIDField(defn *ast.Definition) ast.FieldList {
	fldList := make([]*ast.FieldDefinition, 0)
	for _, fld := range defn.Fields {
		if isIDField(defn, fld) {
			// Deepcopy is not required because we don't modify values other than nonull
			newFld := *fld
			fldList = append(fldList, &newFld)
			break
		}
	}
	return fldList
}

func genArgumentsString(args ast.ArgumentDefinitionList) string {
	if args == nil || len(args) == 0 {
		return ""
	}

	var argsStrs []string

	for _, arg := range args {
		argsStrs = append(argsStrs, genArgumentString(arg))
	}

	sort.Slice(argsStrs, func(i, j int) bool { return argsStrs[i] < argsStrs[j] })
	return fmt.Sprintf("(%s)", strings.Join(argsStrs, ","))
}

func genFieldsString(flds ast.FieldList) string {
	if flds == nil {
		return ""
	}

	sort.Slice(flds, func(i, j int) bool { return flds[i].Name < flds[j].Name })

	var sch strings.Builder

	for _, fld := range flds {
		// Some extra types are generated by gqlparser for internal purpose.
		if !strings.HasPrefix(fld.Name, "__") {
			sch.WriteString(genFieldString(fld))
		}
	}

	return sch.String()
}

func genFieldString(fld *ast.FieldDefinition) string {
	return fmt.Sprintf(
		"\t%s%s: %s %s\n",
		fld.Name, genArgumentsString(fld.Arguments),
		fld.Type.String(), genDirectivesString(fld.Directives),
	)
}

func genDirectivesString(direcs ast.DirectiveList) string {
	var sch strings.Builder
	if len(direcs) == 0 {
		return ""
	}

	var directives []string
	for _, dir := range direcs {
		directives = append(directives, genDirectiveString(dir))
	}

	sort.Slice(directives, func(i, j int) bool { return directives[i] < directives[j] })
	// Assuming multiple directives are space separated.
	sch.WriteString(strings.Join(directives, " "))

	return sch.String()
}

func genDirectiveString(dir *ast.Directive) string {
	return fmt.Sprintf("@%s%s", dir.Name, genDirectiveArgumentsString(dir.Arguments))
}

func genDirectiveArgumentsString(args ast.ArgumentList) string {
	var direcArgs []string
	var sch strings.Builder

	sch.WriteString("(")
	for _, arg := range args {
		direcArgs = append(direcArgs, fmt.Sprintf("%s:\"%s\"", arg.Name, arg.Value.Raw))
	}

	sort.Slice(direcArgs, func(i, j int) bool { return direcArgs[i] < direcArgs[j] })
	sch.WriteString(strings.Join(direcArgs, ",") + ")")

	return sch.String()
}

func genArgumentString(arg *ast.ArgumentDefinition) string {
	return fmt.Sprintf("%s: %s", arg.Name, arg.Type.String())
}

func genInputString(typ *ast.Definition) string {
	return fmt.Sprintf("input %s {\n%s}\n", typ.Name, genFieldsString(typ.Fields))
}

func genEnumString(typ *ast.Definition) string {
	var sch strings.Builder

	valList := typ.EnumValues
	sort.Slice(valList, func(i, j int) bool { return valList[i].Name < valList[j].Name })

	sch.WriteString(fmt.Sprintf("enum %s {\n", typ.Name))
	for _, val := range typ.EnumValues {
		if !strings.HasPrefix(val.Name, "__") {
			sch.WriteString(fmt.Sprintf("\t%s\n", val.Name))
		}
	}
	sch.WriteString("}\n")

	return sch.String()
}

func genObjectString(typ *ast.Definition) string {
	return fmt.Sprintf("type %s {\n%s}\n", typ.Name, genFieldsString(typ.Fields))
}

func genScalarString(typ *ast.Definition) string {
	var sch strings.Builder

	sch.WriteString(fmt.Sprintf("scalar %s\n", typ.Name))
	return sch.String()
}

func genDirectiveDefnString(dir *ast.DirectiveDefinition) string {
	var args, locations []string

	for _, arg := range dir.Arguments {
		args = append(args, fmt.Sprintf("%s: %s", arg.Name, arg.Type.String()))
	}
	sort.Slice(args, func(i, j int) bool { return args[i] < args[j] })

	for _, loc := range dir.Locations {
		locations = append(locations, string(loc))
	}
	sort.Slice(locations, func(i, j int) bool { return locations[i] < locations[j] })

	if len(args) == 0 {
		return fmt.Sprintf(
			"directive @%s on %s\n", dir.Name, strings.Join(locations, ","),
		)
	}

	return fmt.Sprintf(
		"directive @%s(%s) on %s\n", dir.Name, strings.Join(args, ","), strings.Join(locations, ","),
	)
}

func genDirectivesDefnString(direcs map[string]*ast.DirectiveDefinition) string {
	var sch strings.Builder
	if direcs == nil || len(direcs) == 0 {
		return ""
	}

	for _, dir := range direcs {
		sch.WriteString(genDirectiveDefnString(dir))
	}

	return sch.String()
}

// Stringify returns entire schema in string format
func Stringify(sch *ast.Schema) string {
	var schStr, object, scalar, input, query, mutation, enum, direcDefn strings.Builder

	if sch.Types == nil {
		return ""
	}

	var keys []string
	for k := range sch.Types {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })

	for _, key := range keys {
		typ := sch.Types[key]

		if typ.Kind == ast.Object {
			object.WriteString(genObjectString(typ) + "\n")
		} else if typ.Kind == ast.Scalar {
			scalar.WriteString(genScalarString(typ))
		} else if typ.Kind == ast.InputObject {
			input.WriteString(genInputString(typ) + "\n")
		} else if typ.Kind == ast.Enum {
			enum.WriteString(genEnumString(typ) + "\n")
		}
	}

	if sch.Query != nil {
		query.WriteString(genObjectString(sch.Query))
	}

	if sch.Mutation != nil {
		mutation.WriteString(genObjectString(sch.Mutation))
	}

	if sch.Directives != nil {
		direcDefn.WriteString(genDirectivesDefnString(sch.Directives))
	}

	schStr.WriteString("#######################\n# Generated Types\n#######################\n")
	schStr.WriteString(object.String())
	schStr.WriteString("#######################\n# Scalar Definitions\n#######################\n")
	schStr.WriteString(scalar.String())
	schStr.WriteString("#######################\n# Directive Definitions\n#######################\n")
	schStr.WriteString(direcDefn.String())
	schStr.WriteString("#######################\n# Enum Definitions\n#######################\n")
	schStr.WriteString(enum.String())
	schStr.WriteString("#######################\n# Input Definitions\n#######################\n")
	schStr.WriteString(input.String())
	schStr.WriteString("#######################\n# Generated Query\n#######################\n")
	schStr.WriteString(query.String())
	schStr.WriteString("#######################\n# Generated Mutations\n#######################\n")
	schStr.WriteString(mutation.String())

	return schStr.String()
}

func idTypeFor(defn *ast.Definition) string {
	return "ID"
}

func isIDField(defn *ast.Definition, fld *ast.FieldDefinition) bool {
	return fld.Type.Name() == idTypeFor(defn)
}

// Then you can have functions like this to extract things from the directives
// the functions are the behaviors that the rest of the code needs
// ... generally better than encoding the behaviours into the remainder
// of the code - particularly if it's in more than one spot.
//
func getInverseArgs(d *ast.Directive) (string, string, *gqlerror.Error) {
	fldArg := d.Arguments.ForName(inverseFldArg)
	if fldArg == nil {
		panic("Expected a hasInverse to have a field arg, but it did not : " +
			genDirectiveString(d))
		// I think this is a panic.  We are expecting GraphQL validation to have
		// already ensured that this is well formed.  If that hasn't happened
		// then something is broken enough that we need to fix it, not just give
		// an error to the user.
	}

	splitVal := strings.Split(fldArg.Value.Raw, ".")
	if len(splitVal) != 2 {
		return "", "", gqlerror.ErrorPosf(fldArg.Position, "...nice error...")
	}

	return splitVal[0], splitVal[1], nil
}

func getInverseDirective(dirs *ast.DirectiveList) *ast.Directive {
	if dirs == nil {
		return nil
	}
	return dirs.ForName(inverseName)
}

/* With ^^ this, checkHasInverseArgs can be simplified.  ATM part of it is:
----
if invFld.Directives == nil {
	return gqlerror.ErrorPosf(
		fld.Position, "Inverse of %s: %s, doesn't have inverse directive pointing back",
		fld.Name, fldArg.Value.Raw,
	)
}

if invDirective := invFld.Directives.ForName(string(HASINVERSE)); invDirective != nil {
	 if invFldArg := invDirective.Arguments.ForName(string(FIELD)); invFldArg != nil {
					invSplitVal := strings.Split(invFldArg.Value.Raw, ".")
					if len(invSplitVal) == 2 &&
						!(invSplitVal[0] == typ.Name && invSplitVal[1] == fld.Name) {
							........
							........
} else {
	..same error as 3 if's above...
}
--------

that becomes just

d := getInverseDirective(invFld.Directives)
if d == nil { return ...nice error... }

typ, fld := getInverseArgs(d)
if (typ != typ.Name || fld != fld.Name) {
	return ...nice error...
}

The original checkHasInverseArgs mixes in some other validation, code to get
args etc, and goes to 5 levels of nesting deep.  That makes the logic of what
it's actually checking really hidden by all the other bits going on.

This way, the function just becomes the logic that it cares about.
*/

/*
We can probably do better than this too.

 	if direc.Name == string(HASINVERSE) {
		return checkHasInverseArgs(typ, fld, direc, sch)
	}

As the number of directives goes up, this becomes a long list of if's, all with
a string compare to a constant and then a known fn call as the central part.

What if each directive in the supportedDirectives array also had a validation
function.  Then all the nasty string compares against known constants all through
the code can disapear, and we can just find the directive in the array and call

supporedDirectives[d].validate(...)
*/
