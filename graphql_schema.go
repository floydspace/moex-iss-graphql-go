package main

import (
	"fmt"
	"log"
	"net/http"
	"reflect"
	"strings"
	"time"

	"github.com/floydspace/moex-iss-graphql-go/utils"
	"github.com/graphql-go/graphql"
	"github.com/iancoleman/strcase"
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
	"datetime": graphql.DateTime,
	"double":   graphql.Float,
	"var":      graphql.String,
	"number":   graphql.Int,
}

type options struct {
	ref               int
	prefix            string
	defaultArgs       map[string]string
	queryNameReplaces map[string]string
}

func generateSchema() *graphql.Schema {
	fields := parallelGenerateQueries([]options{
		options{ref: 5},
		options{ref: 13, prefix: "security"},
		options{ref: 24,
			queryNameReplaces: map[string]string{
				"turnoversprevdate":        "turnoversPreviousDate",
				"turnoverssectors":         "turnoversSectors",
				"turnoverssectorsprevdate": "turnoversSectorsPreviousDate",
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
		options{ref: 160, prefix: "security"},
		options{ref: 214, prefix: "security"},
		options{ref: 95, prefix: "engine",
			defaultArgs: map[string]string{"engine": "stock"},
			queryNameReplaces: map[string]string{
				"turnoversprevdate":        "turnoversPreviousDate",
				"turnoverssectors":         "turnoversSectors",
				"turnoverssectorsprevdate": "turnoversSectorsPreviousDate",
			},
		},
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

		replacedBlockName := blockName
		if val, ok := options.queryNameReplaces[blockName]; ok {
			replacedBlockName = val
		}

		queryName := replacedBlockName
		if options.prefix != reflect.Zero(reflect.TypeOf(options.prefix)).Interface() {
			queryName = strcase.ToLowerCamel(options.prefix) + strcase.ToCamel(replacedBlockName)
		}

		queries[queryName] = &graphql.Field{
			Type:        graphql.NewList(generateType(queryName, gjson.GetBytes(metaResult, strings.ReplaceAll(blockName, `.`, `\.`)).Get("metadata"))),
			Description: block.description,
			Args:        generateArguments(requiredArgs, block.args, options.defaultArgs),
			Resolve: func(p graphql.ResolveParams) (interface{}, error) {
				url := buildURL(path, p.Args, requiredArgs, blockName)
				result, err := utils.FetchBytes(url)
				if err != nil {
					log.Fatalf("failed to fetch data, error: %v", err)
					return nil, err
				}

				return gjson.ParseBytes(result).Array()[1].Get(blockName).Value(), nil
			},
		}
	}

	return
}

func buildURL(path string, args map[string]interface{}, requiredArgs []string, blockName string) string {
	var queryArgs map[string]interface{}
	if err := mergo.Merge(&queryArgs, args); err != nil {
		log.Fatalf("failed to merge gql fields, error: %v", err)
	}

	for _, arg := range requiredArgs {
		path = strings.ReplaceAll(path, "["+arg+"]", queryArgs[arg].(string))
		delete(queryArgs, arg)
	}

	queryParams := []string{
		"iss.meta=off",
		"iss.data=on",
		"iss.json=extended",
		"iss.only=" + blockName,
	}

	for key, val := range queryArgs {
		queryParams = append(queryParams, key+"="+val.(string))
	}

	return fmt.Sprintf("%s/%s.json?%s", baseURL, path, strings.Join(queryParams, "&"))
}

func generateType(queryName string, metadata gjson.Result) graphql.Type {
	var fields = make(graphql.Fields)

	for field, data := range metadata.Map() {
		fields[strcase.ToSnake(field)] = func(field string, issType string) *graphql.Field {
			return &graphql.Field{
				Type: typeMappings[issType],
				Resolve: func(p graphql.ResolveParams) (interface{}, error) {
					return normalizeFieldValue(issType, p.Source.(map[string]interface{})[field]), nil
				},
			}
		}(field, data.Get("type").String())
	}

	return graphql.NewObject(graphql.ObjectConfig{
		Name:   strcase.ToCamel(inflection.Singular(queryName)),
		Fields: fields,
	})
}

func generateArguments(requiredArgs []string, otherArgs []argument, defaultArgs map[string]string) (fieldArgs graphql.FieldConfigArgument) {
	fieldArgs = make(graphql.FieldConfigArgument)
	for _, arg := range requiredArgs {
		fieldArgs[arg] = &graphql.ArgumentConfig{
			Type: graphql.NewNonNull(graphql.String),
		}

		if defaultValue, ok := defaultArgs[arg]; ok {
			fieldArgs[arg].Type = graphql.String
			fieldArgs[arg].DefaultValue = defaultValue
		}
	}

	for _, arg := range otherArgs {
		fieldArgs[arg.name] = &graphql.ArgumentConfig{
			Type:        typeMappings[arg.typ],
			Description: arg.description,
		}
	}

	return
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
