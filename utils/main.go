package utils

import (
	"encoding/json"
	"net/http"
)

// FetchJSON fetches and marshales a json from remote server.
func FetchJSON(url string) (jsonResult interface{}, err error) {
	res, err := http.Get(url)
	if err != nil {
		return
	}
	defer res.Body.Close()

	err = json.NewDecoder(res.Body).Decode(&jsonResult)
	if err != nil {
		return
	}

	return
}
