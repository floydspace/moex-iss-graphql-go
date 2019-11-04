package utils

import (
	"encoding/json"
	"io/ioutil"
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

// FetchBytes fetches content bytes from remote server.
func FetchBytes(url string) (result []byte, err error) {
	res, err := http.Get(url)
	if err != nil {
		return
	}
	defer res.Body.Close()

	result, err = ioutil.ReadAll(res.Body)
	if err != nil {
		return
	}

	return
}
