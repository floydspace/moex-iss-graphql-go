package main

import (
	"log"
	"net/http"

	"github.com/graphql-go/handler"
)

func main() {
	schema := generateSchema()

	http.Handle("/", handler.New(&handler.Config{
		Schema:     schema,
		Pretty:     true,
		Playground: true,
	}))

	log.Print("App is ready at http://localhost:4000/")
	if err := http.ListenAndServe(":4000", nil); err != nil {
		panic(err)
	}
}
