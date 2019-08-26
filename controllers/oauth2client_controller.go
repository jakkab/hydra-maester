/*

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"

	"github.com/go-logr/logr"
	hydrav1alpha1 "github.com/ory/hydra-maester/api/v1alpha1"
	apiv1 "k8s.io/api/core/v1"
	apierrs "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// OAuth2ClientReconciler reconciles a OAuth2Client object
type OAuth2ClientReconciler struct {
	HydraURL   *url.URL
	Log        logr.Logger
	HTTPClient *http.Client
	client.Client
}

// +kubebuilder:rbac:groups=hydra.ory.sh,resources=oauth2clients,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=hydra.ory.sh,resources=oauth2clients/status,verbs=get;update;patch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch;delete

func (r *OAuth2ClientReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	ctx := context.Background()
	_ = r.Log.WithValues("oauth2client", req.NamespacedName)

	var client hydrav1alpha1.OAuth2Client
	if err := r.Get(ctx, req.NamespacedName, &client); err != nil {
		if apierrs.IsNotFound(err) {
			//todo: delete client?
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	var registered = false
	var err error

	if client.Status.ClientID != nil {

		_, registered, err = r.getOAuth2Client(*client.Status.ClientID)
		if err != nil {
			return ctrl.Result{}, err
		}
	}

	if !registered {
		return ctrl.Result{}, r.registerOAuth2Client(ctx, &client)
	}

	return ctrl.Result{}, nil
}

func (r *OAuth2ClientReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&hydrav1alpha1.OAuth2Client{}).
		Complete(r)
}

func (r *OAuth2ClientReconciler) registerOAuth2Client(ctx context.Context, client *hydrav1alpha1.OAuth2Client) error {
	created, err := r.postOAuth2Client(client.ToOAuth2ClientJSON())
	if err != nil {
		return err
	}

	clientSecret := apiv1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      client.Name,
			Namespace: client.Namespace,
		},
		Data: map[string][]byte{
			"client_secret": []byte(*created.Secret),
		},
	}

	err = r.Create(ctx, &clientSecret)
	if err != nil {
		return err
	}

	client.Status.Secret = &clientSecret.Name
	client.Status.ClientID = created.ClientID
	return r.Status().Update(ctx, client)
}

func (r *OAuth2ClientReconciler) getOAuth2Client(id string) (*hydrav1alpha1.OAuth2ClientJSON, bool, error) {

	var jsonClient *hydrav1alpha1.OAuth2ClientJSON

	req, err := r.newRequest(http.MethodGet, id, nil)
	if err != nil {
		return nil, false, err
	}

	resp, err := r.do(req, &jsonClient)
	if err != nil {
		return nil, false, err
	}

	switch resp.StatusCode {
	case http.StatusOK:
		return jsonClient, true, nil
	case http.StatusNotFound:
		return nil, false, nil
	default:
		return nil, false, fmt.Errorf("%s %s http request returned unexpected status code %s", req.Method, req.URL.String(), resp.Status)
	}
}

func (r *OAuth2ClientReconciler) postOAuth2Client(c *hydrav1alpha1.OAuth2ClientJSON) (*hydrav1alpha1.OAuth2ClientJSON, error) {

	var jsonClient *hydrav1alpha1.OAuth2ClientJSON

	req, err := r.newRequest(http.MethodPost, "", c)
	if err != nil {
		return nil, err
	}

	resp, err := r.do(req, &jsonClient)
	if err != nil {
		return nil, err
	}

	switch resp.StatusCode {
	case http.StatusCreated:
		return jsonClient, nil
	case http.StatusConflict:
		return nil, fmt.Errorf(" %s %s http request failed: requested ID already exists", req.Method, req.URL)
	default:
		return nil, fmt.Errorf("%s %s http request returned unexpected status code: %s", req.Method, req.URL, resp.Status)
	}
}

func (r *OAuth2ClientReconciler) newRequest(method, relativePath string, body interface{}) (*http.Request, error) {

	var buf io.ReadWriter
	if body != nil {
		buf = new(bytes.Buffer)
		err := json.NewEncoder(buf).Encode(body)
		if err != nil {
			return nil, err
		}
	}

	r.HydraURL.Path = path.Join(r.HydraURL.Path, relativePath)

	req, err := http.NewRequest(method, r.HydraURL.String(), buf)
	if err != nil {
		return nil, err
	}

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")

	return req, nil

}

func (r *OAuth2ClientReconciler) do(req *http.Request, v interface{}) (*http.Response, error) {
	resp, err := r.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()
	return resp, json.NewDecoder(resp.Body).Decode(v)
}
