package main

import (
	"io"
	"log"
	"regexp"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

type block struct {
	name        string
	description string
	args        []argument
}

type argument struct {
	name        string
	description string
	typ         string
}

func parseIssReference(body io.Reader) (path string, requiredArgs []string, blocks []block) {
	doc, err := goquery.NewDocumentFromReader(body)
	if err != nil {
		log.Fatalf("failed to parse reference body, error: %v", err)
	}

	headerText := doc.Find("body > h1").Text()
	path = regexp.MustCompile(`\/iss\/(.*)`).FindStringSubmatch(headerText)[1]
	requiredArgs = parseRequiredArguments(path)

	doc.Find("body > dl > dt").Each(func(_ int, blockSelector *goquery.Selection) {
		blockArgsSelector := blockSelector.Next()
		argsSelector := blockArgsSelector.Find("dl > dt")

		var args []argument
		argsSelector.Each(func(_ int, argSelector *goquery.Selection) {
			argMeta := argSelector.Next()
			contents := argMeta.Contents()
			typeLabelSelector := argMeta.Find("strong:contains('Type:')")
			args = append(args, argument{
				name:        argSelector.Text(),
				description: strings.TrimSpace(argMeta.ChildrenFiltered("pre").Text()),
				typ:         contents.Get(contents.IndexOfSelection(typeLabelSelector) + 1).Data,
			})
		})

		blocks = append(blocks, block{
			name:        strings.Split(blockSelector.Text(), " ")[0],
			description: strings.TrimSpace(blockArgsSelector.ChildrenFiltered("pre").Text()),
			args:        args,
		})
	})

	return
}

func parseRequiredArguments(path string) (arguments []string) {
	re := regexp.MustCompile(`\[(\w+)\]`)

	for _, arg := range re.FindAllStringSubmatch(path, 10) {
		arguments = append(arguments, arg[1])
	}

	return
}
