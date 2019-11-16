package main

import (
	"fmt"
	"log"
	"net/http"
	"reflect"
	"strings"
	"time"

	"github.com/floydspace/moex-iss-graphql-go/utils"
	"github.com/floydspace/strcase"
	"github.com/graphql-go/graphql"
	"github.com/graphql-go/graphql/language/ast"
	"github.com/imdario/mergo"
	"github.com/jinzhu/inflection"
	"github.com/tidwall/gjson"
)

var baseURL = "https://iss.moex.com/iss"

var typeMappings = map[string]*graphql.Scalar{
	"int32":    graphql.Int,
	"int64":    graphql.Int,
	"string":   graphql.String,
	"date":     graphql.String,
	"time":     graphql.String,
	"datetime": graphql.DateTime,
	"double":   graphql.Float,
	"var":      graphql.String,
	"number":   graphql.Int,
	"bool":     graphql.Boolean,
}

type options struct {
	ref               int
	prefix            string
	suffix            string
	defaultArgs       map[string]string
	enumArgs          map[string][]string
	argTypeReplaces   map[string]string
	queryNameReplaces map[string]string
}

var commonInputTypes = map[string]graphql.Input{
	"lang": generateEnum("Language", []string{"ru", "en"}),
}

var commonOutputTypes = map[string]*graphql.Object{
	"engine": graphql.NewObject(graphql.ObjectConfig{Name: "Engine", Fields: make(graphql.Fields)}),
}

func generateSchema() *graphql.Schema {
	fields := parallelGenerateQueries([]options{
		options{ref: 5,
			enumArgs: map[string][]string{
				"group_by": {"group", "type"},
			},
			argTypeReplaces: map[string]string{
				"limit":      "number",
				"is_trading": "bool",
			},
		},
		options{ref: 13, prefix: "security"},
		options{ref: 40},
		options{ref: 41,
			defaultArgs: map[string]string{"engine": "stock"},
			queryNameReplaces: map[string]string{
				"dailytable": "dailyTable",
				"timetable":  "timeTable",
			},
		},
		options{ref: 24,
			queryNameReplaces: map[string]string{
				"turnoversprevdate":        "turnoversPreviousDate",
				"turnoverssectors":         "turnoversSectors",
				"turnoverssectorsprevdate": "turnoversSectorsPreviousDate",
			},
			argTypeReplaces: map[string]string{
				"is_tonight_session": "bool",
			},
		},
		options{ref: 28,
			queryNameReplaces: map[string]string{
				"boardgroups":         "boardGroups",
				"securitytypes":       "securityTypes",
				"securitygroups":      "securityGroups",
				"securitycollections": "securityCollections",
			},
		},
		options{ref: 160, prefix: "security",
			argTypeReplaces: map[string]string{
				"only_actual": "bool",
			},
		},
		options{ref: 214, prefix: "security"},
		options{ref: 95, prefix: "engine",
			defaultArgs: map[string]string{"engine": "stock"},
			queryNameReplaces: map[string]string{
				"turnoversprevdate":        "turnoversPreviousDate",
				"turnoverssectors":         "turnoversSectors",
				"turnoverssectorsprevdate": "turnoversSectorsPreviousDate",
			},
			argTypeReplaces: map[string]string{
				"is_tonight_session": "bool",
			},
		},
		options{ref: 96, prefix: "market",
			defaultArgs: map[string]string{"engine": "stock", "market": "shares"},
		},
		options{ref: 100, suffix: "columns"},
	})

	rootQuery := graphql.NewObject(graphql.ObjectConfig{
		Name:   "RootQuery",
		Fields: fields,
	})

	schema, err := graphql.NewSchema(graphql.SchemaConfig{
		Query: rootQuery,
	})

	if err != nil {
		log.Fatalf("failed to create new schema, error: %v", err)
	}

	return &schema
}

func parallelGenerateQueries(queryOptions []options) (fields graphql.Fields) {
	channel := make(chan graphql.Fields)
	defer close(channel)

	for _, opts := range queryOptions {
		go func(opts options) { channel <- generateQueries(opts) }(opts)
	}

	for count := 0; count < len(queryOptions); count++ {
		if err := mergo.Merge(&fields, <-channel); err != nil {
			log.Fatalf("failed to merge gql fields, error: %v", err)
		}
	}

	return
}

func generateQueries(options options) (queries graphql.Fields) {
	log.Printf("Generating queries for reference %d", options.ref)

	queries = make(graphql.Fields)

	refURL := fmt.Sprintf("%s/reference/%d", baseURL, options.ref)

	res, err := http.Get(refURL)
	if err != nil {
		log.Fatalf("failed to fetch reference, error: %v", err)
	}

	path, requiredArgs, blocks := parseIssReference(res.Body)
	res.Body.Close()

	pathWithDefaultArgs := path
	for arg, val := range options.defaultArgs {
		pathWithDefaultArgs = strings.ReplaceAll(pathWithDefaultArgs, "["+arg+"]", val)
	}

	refMetaURL := fmt.Sprintf("%s/%s.json?iss.meta=on&iss.data=off", baseURL, pathWithDefaultArgs)
	metaResult, err := utils.FetchBytes(refMetaURL)
	if err != nil {
		log.Fatalf("failed to fetch reference metadata, error: %v", err)
	}

	for _, block := range blocks {
		blockName := block.name
		escapedBlockName := strings.ReplaceAll(block.name, `.`, `\.`)

		replacedBlockName := blockName
		if val, ok := options.queryNameReplaces[blockName]; ok {
			replacedBlockName = val
		}

		queryName := replacedBlockName
		if options.prefix != reflect.Zero(reflect.TypeOf(options.prefix)).Interface() {
			queryName = strcase.ToLowerCamel(options.prefix) + strcase.ToCamel(replacedBlockName)
		}
		if options.suffix != reflect.Zero(reflect.TypeOf(options.suffix)).Interface() {
			queryName = strcase.ToLowerCamel(replacedBlockName) + strcase.ToCamel(options.suffix)
		}

		queries[queryName] = &graphql.Field{
			Type:        graphql.NewList(generateType(queryName, gjson.GetBytes(metaResult, escapedBlockName).Get("metadata"))),
			Description: block.description,
			Args:        generateArguments(requiredArgs, block.args, options),
			Resolve: func(p graphql.ResolveParams) (interface{}, error) {
				url := buildURL(path, p.Args, requiredArgs, blockName, p.Info)
				result, err := utils.FetchBytes(url)
				if err != nil {
					log.Fatalf("failed to fetch data, error: %v", err)
					return nil, err
				}

				if len(result) == 0 {
					return []interface{}{}, nil
				}

				return gjson.ParseBytes(result).Array()[1].Get(escapedBlockName).Value(), nil
			},
		}
	}

	return
}

func buildURL(path string, args map[string]interface{}, requiredArgs []string, blockName string, info graphql.ResolveInfo) string {
	var queryArgs map[string]interface{}
	if err := mergo.Merge(&queryArgs, args); err != nil {
		log.Fatalf("failed to merge gql fields, error: %v", err)
	}

	for _, arg := range requiredArgs {
		path = strings.ReplaceAll(path, "["+arg+"]", queryArgs[arg].(string))
		delete(queryArgs, arg)
	}

	// Cannot handle requeset fields properly, due to column names in iss are case sensetive
	// requestedFields := getRequestedFields(info)

	queryParams := []string{
		"iss.meta=off",
		"iss.data=on",
		"iss.json=extended",
		"iss.only=" + blockName,
		// blockName + ".columns=" + strings.Join(requestedFields, ","),
	}

	for key, val := range queryArgs {
		queryParams = append(queryParams, key+"="+fmt.Sprint(val))
	}

	return fmt.Sprintf("%s/%s.json?%s", baseURL, path, strings.Join(queryParams, "&"))
}

func getRequestedFields(info graphql.ResolveInfo) (fields []string) {
	queryKey := info.Path.Key.(string)

	for _, querySelection := range info.Operation.GetSelectionSet().Selections {
		query := querySelection.(*ast.Field)
		if (query.Alias != nil && query.Alias.Value == queryKey) || query.Name.Value == queryKey {
			for _, fieldSelection := range query.GetSelectionSet().Selections {
				field := fieldSelection.(*ast.Field)
				fields = append(fields, field.Name.Value)
			}
			break
		}
	}

	return
}

func generateType(queryName string, metadata gjson.Result) (gqlType *graphql.Object) {
	if gqlObject, ok := commonOutputTypes[inflection.Singular(queryName)]; ok {
		gqlType = gqlObject
	} else {
		gqlType = graphql.NewObject(graphql.ObjectConfig{
			Name:   strcase.ToCamel(inflection.Singular(queryName)),
			Fields: make(graphql.Fields),
		})
	}

	for field, data := range metadata.Map() {
		issType := data.Get("type").String()
		gqlType.AddFieldConfig(
			strcase.ToSnake(field),
			&graphql.Field{
				Type: typeMappings[issType],
				Resolve: func(p graphql.ResolveParams) (result interface{}, err error) {
					for key, val := range p.Source.(map[string]interface{}) {
						if strcase.ToSnake(key) == p.Info.FieldName {
							result = normalizeFieldValue(issType, val)
							break
						}
					}
					return
				},
			},
		)
	}

	return
}

func generateArguments(requiredArgs []string, otherArgs []argument, options options) (fieldArgs graphql.FieldConfigArgument) {
	fieldArgs = make(graphql.FieldConfigArgument)
	for _, arg := range requiredArgs {
		fieldArgs[arg] = &graphql.ArgumentConfig{
			Type: graphql.NewNonNull(graphql.String),
		}

		if defaultValue, ok := options.defaultArgs[arg]; ok {
			fieldArgs[arg].Type = graphql.String
			fieldArgs[arg].DefaultValue = defaultValue
		}
	}

	for _, arg := range otherArgs {
		argType := arg.typ

		if replacedArgType, ok := options.argTypeReplaces[arg.name]; ok {
			argType = replacedArgType
		}

		var gqlType graphql.Input = typeMappings[argType]

		if inputType, ok := commonInputTypes[arg.name]; ok {
			gqlType = inputType
		}

		if argEnum, ok := options.enumArgs[arg.name]; ok {
			gqlType = generateEnum(arg.name, argEnum)
		}

		fieldArgs[arg.name] = &graphql.ArgumentConfig{
			Type:        gqlType,
			Description: arg.description,
		}
	}

	return
}

func generateEnum(argName string, argEnum []string) *graphql.Enum {
	enumValues := make(map[string]*graphql.EnumValueConfig)

	for _, val := range argEnum {
		enumValues[strcase.ToScreamingSnake(val)] = &graphql.EnumValueConfig{Value: val}
	}

	return graphql.NewEnum(graphql.EnumConfig{
		Name:   strcase.ToCamel(argName),
		Values: enumValues,
	})
}

func normalizeFieldValue(typ string, value interface{}) interface{} {
	if typ == "datetime" && value != nil {
		datetime, err := time.Parse("2006-01-02 15:04:05", value.(string))
		if err != nil {
			log.Fatalf("failed parse datetime string, error: %v", err)
		}

		return datetime
	}

	return value
}
