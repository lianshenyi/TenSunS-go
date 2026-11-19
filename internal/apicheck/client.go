// Package apicheck implements the API contract checks used by the local scripts.
package apicheck

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type object = map[string]any

// Client contacts an explicitly selected TenSunS API.
type Client struct {
	base  string
	http  *http.Client
	token string
}

// New creates a client that reports check failures without printing response data.
func New(base string) *Client {
	return &Client{
		base: strings.TrimRight(base, "/"),
		http: &http.Client{
			Timeout: 30 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

// Login validates the admin login envelope and stores the authorization header.
func (c *Client) Login(password string) error {
	c.token = ""
	body, err := json.Marshal(map[string]string{"username": "admin", "password": password})
	if err != nil {
		return err
	}
	response, err := c.request(http.MethodPost, "/api/user/login", "", body)
	if err != nil {
		return err
	}
	data, _ := response["data"].(object)
	token, _ := data["token"].(string)
	if !strings.HasPrefix(token, "Bearer ") || token == "Bearer " {
		return errors.New("/api/user/login: invalid bearer token")
	}
	c.token = token
	return nil
}

// CheckUserInfo validates the admin name and avatar. The standalone identity
// check also requires the exact data envelope, including roles.
func (c *Client) CheckUserInfo(exact bool) error {
	const path = "/api/user/info"
	response, err := c.get(path, "")
	if err != nil {
		return err
	}
	data, _ := response["data"].(object)
	if data["name"] != "admin" || data["avatar"] != "/harbor-mark.svg" {
		return errors.New(path + ": unexpected admin name or avatar")
	}
	if exact {
		roles, _ := data["roles"].([]any)
		if len(data) != 3 || len(roles) != 1 || roles[0] != "admin" {
			return errors.New(path + ": unexpected identity fields or roles")
		}
	}
	return nil
}

func (c *Client) get(path, query string) (object, error) {
	return c.request(http.MethodGet, path, query, nil)
}

func (c *Client) request(method, path, query string, body []byte) (object, error) {
	req, err := http.NewRequest(method, c.base+path+query, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("%s: invalid target URL", path)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s: HTTP request failed", path)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: HTTP %d", path, resp.StatusCode)
	}

	decoder := json.NewDecoder(resp.Body)
	decoder.UseNumber()
	var result object
	if err := decoder.Decode(&result); err != nil || decoder.Decode(new(any)) != io.EOF {
		return nil, fmt.Errorf("%s: invalid JSON response", path)
	}
	code, _ := result["code"].(json.Number)
	numericCode, _ := code.Float64()
	if numericCode != 20000 {
		return nil, fmt.Errorf("%s: expected business code 20000", path)
	}
	return result, nil
}
