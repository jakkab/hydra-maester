package controllers

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/ory/hydra-maester/api/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/square/go-jose.v2/json"
	ctrl "sigs.k8s.io/controller-runtime"
)

func TestNewRequest(t *testing.T) {

	r := OAuth2ClientReconciler{
		HydraURL: url.URL{
			Path:   "example.com",
			Scheme: "http",
		}}

	assert := assert.New(t)

	for desc, tc := range map[string]struct {
		method  string
		relPath string
		body    interface{}
	}{
		"Basic GET request": {
			method:  http.MethodGet,
			relPath: "some-endpoint",
			body:    nil,
		},
		"Basic POST request": {
			method:  http.MethodPost,
			relPath: "",
			body:    v1alpha1.OAuth2ClientJSON{Name: "some_name", GrantTypes: []string{"type1"}, Scope: "some,scope"},
		},
	} {
		t.Run(fmt.Sprintf("case/%s", desc), func(t *testing.T) {

			//when
			req, err := r.newRequest(tc.method, tc.relPath, tc.body)

			//then
			require.NoError(t, err)
			assert.Equal(tc.method, req.Method)
			if tc.relPath == "" {
				assert.Equal(r.HydraURL.String(), req.URL.String())
			} else {
				assert.Equal(fmt.Sprintf("%s/%s", r.HydraURL.String(), tc.relPath), req.URL.String())
			}

			require.NotEmpty(t, req.Header.Get("Accept"))
			assert.Equal("application/json", req.Header.Get("Accept"))

			if tc.body != nil {
				require.NotEmpty(t, req.Header.Get("Content-Type"))
				assert.Equal("application/json", req.Header.Get("Content-Type"))

				buf := new(bytes.Buffer)
				_, _ = buf.ReadFrom(req.Body)

				var c v1alpha1.OAuth2ClientJSON
				err = json.Unmarshal(buf.Bytes(), &c)

				assert.Equal(c, tc.body)
			}
		})
	}
}

const (
	testID            = "test-id"
	schemeHTTP        = "http"
	testClient        = `{"client_id":"test-id","client_name":"test-name","scope":"some,scopes","grant_types":["type1"]}`
	testClientCreated = `{"client_id":"test-id-2", "client_secret": "TmGkvcY7k526","client_name":"test-name-2","scope":"some,other,scopes","grant_types":["type2"]}`
	emptyBody         = `{}`
)

type server struct {
	statusCode int
	respBody   string
	err        error
}

var testOAuthJSON = &v1alpha1.OAuth2ClientJSON{
	Name:       "test-name-2",
	Scope:      "some,other,scopes",
	GrantTypes: []string{"type2"},
}

func TestCRUD(t *testing.T) {

	assert := assert.New(t)

	r := OAuth2ClientReconciler{
		HTTPClient: &http.Client{},
		HydraURL:   url.URL{Scheme: schemeHTTP},
		Log:        ctrl.Log.WithName("test").WithName("OAuth2Client"),
	}

	t.Run("method=get", func(t *testing.T) {

		for d, tc := range map[string]server{
			"getting registered client": {
				http.StatusOK,
				testClient,
				nil,
			},
			"getting unregistered client": {
				http.StatusNotFound,
				emptyBody,
				nil,
			},
			"internal server error when requesting": {
				http.StatusInternalServerError,
				emptyBody,
				errors.New("http request returned unexpected status code"),
			},
		} {
			t.Run(fmt.Sprintf("case/%s", d), func(t *testing.T) {

				//given
				shouldFind := tc.statusCode == http.StatusOK

				h := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					assert.Equal(fmt.Sprintf("%s/%s", r.HydraURL.String(), testID), fmt.Sprintf("%s://%s%s", schemeHTTP, req.Host, req.URL.Path))
					assert.Equal(http.MethodGet, req.Method)
					w.WriteHeader(tc.statusCode)
					w.Write([]byte(tc.respBody))
					if shouldFind {
						w.Header().Set("Content-type", "application/json")
					}
				})

				s := httptest.NewServer(h)
				serverUrl, _ := url.Parse(s.URL)
				r.HydraURL = *serverUrl.ResolveReference(&url.URL{Path: "/clients"})

				//when
				c, found, err := r.getOAuth2Client(testID)

				//then
				if tc.err == nil {
					require.NoError(t, err)
				} else {
					require.Error(t, err)
					assert.Contains(err.Error(), tc.err.Error())
				}

				assert.Equal(shouldFind, found)
				if shouldFind {
					require.NotNil(t, c)
					var expected v1alpha1.OAuth2ClientJSON
					json.Unmarshal([]byte(testClient), &expected)
					assert.Equal(&expected, c)
				}
			})
		}
	})

	t.Run("method=post", func(t *testing.T) {

		for d, tc := range map[string]server{
			"with new client": {
				http.StatusCreated,
				testClientCreated,
				nil,
			},
			"with existing client": {
				http.StatusConflict,
				emptyBody,
				errors.New("requested ID already exists"),
			},
			"internal server error when requesting": {
				http.StatusInternalServerError,
				emptyBody,
				errors.New("http request returned unexpected status code"),
			},
		} {
			t.Run(fmt.Sprintf("case/%s", d), func(t *testing.T) {

				//given
				new := tc.statusCode == http.StatusCreated

				h := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					assert.Equal(r.HydraURL.String(), fmt.Sprintf("%s://%s%s", schemeHTTP, req.Host, req.URL.Path))
					assert.Equal(http.MethodPost, req.Method)
					w.WriteHeader(tc.statusCode)
					w.Write([]byte(tc.respBody))
					if new {
						w.Header().Set("Content-type", "application/json")
					}
				})

				s := httptest.NewServer(h)
				serverUrl, _ := url.Parse(s.URL)
				r.HydraURL = *serverUrl.ResolveReference(&url.URL{Path: "/clients"})

				//when
				c, err := r.postOAuth2Client(testOAuthJSON)

				//then
				if tc.err == nil {
					require.NoError(t, err)
				} else {
					require.Error(t, err)
					assert.Contains(err.Error(), tc.err.Error())
				}

				if new {
					require.NotNil(t, c)

					assert.Equal(testOAuthJSON.Name, c.Name)
					assert.Equal(testOAuthJSON.Scope, c.Scope)
					assert.Equal(testOAuthJSON.GrantTypes, c.GrantTypes)
					assert.NotNil(c.Secret)
					assert.NotNil(c.ClientID)
				}
			})
		}
	})

	t.Run("method=delete", func(t *testing.T) {

		for d, tc := range map[string]server{
			"with registered client": {
				statusCode: http.StatusNoContent,
			},
			"with unregistered client": {
				statusCode: http.StatusNotFound,
			},
			"internal server error when requesting": {
				statusCode: http.StatusInternalServerError,
				err:        errors.New("http request returned unexpected status code"),
			},
		} {
			t.Run(fmt.Sprintf("case/%s", d), func(t *testing.T) {

				//given
				h := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					assert.Equal(fmt.Sprintf("%s/%s", r.HydraURL.String(), testID), fmt.Sprintf("%s://%s%s", schemeHTTP, req.Host, req.URL.Path))
					assert.Equal(http.MethodDelete, req.Method)
					w.WriteHeader(tc.statusCode)
				})

				s := httptest.NewServer(h)
				serverUrl, _ := url.Parse(s.URL)
				r.HydraURL = *serverUrl.ResolveReference(&url.URL{Path: "/clients"})

				//when
				err := r.deleteOAuth2Client(testID)

				//then
				if tc.err == nil {
					require.NoError(t, err)
				} else {
					require.Error(t, err)
					assert.Contains(err.Error(), tc.err.Error())
				}
			})
		}
	})
}
