// Copyright 2014 CoreOS, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package auth provides Google oauth2 bindings for mantle.
package auth

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/user"
	"path/filepath"

	"github.com/coreos/mantle/Godeps/_workspace/src/golang.org/x/oauth2"
	"github.com/coreos/mantle/Godeps/_workspace/src/golang.org/x/oauth2/google"
)

// client registered under 'marineam-tools'
var conf = oauth2.Config{
	ClientID:     "937427706989-nbndmfkp0knqardoagk6lbcamrsh828i.apps.googleusercontent.com",
	ClientSecret: "F6Xs5wGHZzGw-QFXl3aylLUT",
	Endpoint: oauth2.Endpoint{
		AuthURL:  "https://accounts.google.com/o/oauth2/auth",
		TokenURL: "https://accounts.google.com/o/oauth2/token",
	},
	RedirectURL: "urn:ietf:wg:oauth:2.0:oob",
	Scopes: []string{"https://www.googleapis.com/auth/devstorage.full_control",
		"https://www.googleapis.com/auth/compute"},
}

func writeCache(cachePath string, tok *oauth2.Token) error {
	file, err := os.OpenFile(cachePath, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	defer file.Close()

	if err := json.NewEncoder(file).Encode(tok); err != nil {
		return err
	}
	return nil
}

func readCache(cachePath string) (*oauth2.Token, error) {
	file, err := os.Open(cachePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	tok := &oauth2.Token{}
	if err := json.NewDecoder(file).Decode(tok); err != nil {
		return nil, err
	}

	// make sure token is refreshable
	if tok != nil && !tok.Valid() {
		ts := conf.TokenSource(oauth2.NoContext, tok)
		tok, err = ts.Token()
		if err != nil || !tok.Valid() {
			fmt.Printf("Could not refresh cached token: %v\n", err)
			return nil, nil
		}
	}
	return tok, nil
}

func getToken() (*oauth2.Token, error) {
	userInfo, err := user.Current()
	if err != nil {
		return nil, err
	}

	cachePath := filepath.Join(userInfo.HomeDir, ".mantle-cache-google.json")
	tok, err := readCache(cachePath)
	if err != nil {
		log.Printf("Error reading google token cache file: %v", err)
	}
	if tok == nil {
		url := conf.AuthCodeURL("state", oauth2.AccessTypeOffline)
		fmt.Printf("Visit the URL for the auth dialog: %v\n", url)
		fmt.Print("Enter token: ")

		var code string
		if _, err := fmt.Scan(&code); err != nil {
			return nil, err
		}
		tok, err = conf.Exchange(oauth2.NoContext, code)
		if err != nil {
			return nil, err
		}
		err = writeCache(cachePath, tok)
		if err != nil {
			log.Printf("Error writing google token cache file: %v", err)
		}
	}
	return tok, nil
}

// GoogleClient provides an http.Client authorized with an oauth2 token
// that is automatically cached and refreshed from a file named
// '.mantle-cache-google.json'. This uses interactive oauth2
// authorization and requires a user follow to follow a web link and
// paste in an authorization token.
func GoogleClient() (*http.Client, error) {
	tok, err := getToken()
	if err != nil {
		return nil, err
	}
	return conf.Client(oauth2.NoContext, tok), nil
}

// GoogleTokenSource provides an outh2.TokenSource authorized in the
// same manner as GoogleClient.
func GoogleTokenSource() (oauth2.TokenSource, error) {
	tok, err := getToken()
	if err != nil {
		return nil, err
	}
	return conf.TokenSource(oauth2.NoContext, tok), nil
}

// GoogleServiceClient fetchs a token from Google Compute Engine's
// metadata service. This should be used on GCE vms. The Default account
// is used.
func GoogleServiceClient() *http.Client {
	return &http.Client{
		Transport: &oauth2.Transport{
			Source: google.ComputeTokenSource(""),
		},
	}
}

// GoogleServiceTokenSource provides an oauth2.TokenSource authorized in
// the same manner as GoogleServiceClient().
func GoogleServiceTokenSource() oauth2.TokenSource {
	return google.ComputeTokenSource("")
}

// GoogleClientFromJSONKey  provides an http.Client authorized with an
// oauth2 token retrieved using a Google Developers service account's
// private JSON key file.
func GoogleClientFromJSONKey(jsonKey []byte, scope ...string) (*http.Client, error) {
	if scope == nil {
		scope = conf.Scopes
	}
	jwtConf, err := google.JWTConfigFromJSON(jsonKey, scope...)
	if err != nil {
		return nil, err
	}

	return jwtConf.Client(oauth2.NoContext), nil

}

// GoogleTokenSourceFromJSONKey provides an oauth2.TokenSource
// authorized in the same manner as GoogleClientFromJSONKey.
func GoogleTokenSourceFromJSONKey(jsonKey []byte, scope ...string) (oauth2.TokenSource, error) {
	if scope == nil {
		scope = conf.Scopes
	}

	jwtConf, err := google.JWTConfigFromJSON(jsonKey, scope...)
	if err != nil {
		return nil, err
	}

	return jwtConf.TokenSource(oauth2.NoContext), nil
}
