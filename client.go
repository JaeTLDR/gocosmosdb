package gocosmosdb

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/davecgh/go-spew/spew"
	"github.com/intwinelabs/logger"
	"github.com/moul/http2curl"
)

type Clienter interface {
	Read(link string, ret interface{}) error
	Delete(link string) error
	Query(link string, query string, ret interface{}) error
	Create(link string, body, ret interface{}) error
	Replace(link string, body, ret interface{}) error
	ReplaceAsync(link string, body, ret interface{}) error
	Execute(link string, body, ret interface{}) error
	GetURI() string
	GetConfig() Config
	EnableDebug()
	DisableDebug()
}

type Client struct {
	Url    string
	Config Config
	http.Client
	Logger *logger.Logger
}

// GetURI return a clients URI
func (c *Client) GetURI() string {
	return c.Url
}

// GetConfig return a clients URI
func (c *Client) GetConfig() Config {
	return c.Config
}

// EnableDebug enables the CosmosDB debug in config
func (c *Client) EnableDebug() {
	c.Config.Debug = true
}

// DisableDebug disables the CosmosDB debug in config
func (c *Client) DisableDebug() {
	c.Config.Debug = false
}

// Read resource by self link
func (c *Client) Read(link string, ret interface{}) error {
	return c.method("GET", link, http.StatusOK, ret, &bytes.Buffer{}, nil)
}

// Delete resource by self link
func (c *Client) Delete(link string) error {
	return c.method("DELETE", link, http.StatusNoContent, nil, &bytes.Buffer{}, nil)
}

// Query resource
func (c *Client) Query(link, query string, ret interface{}) error {
	buf := bytes.NewBufferString(querify(query))
	req, err := http.NewRequest("POST", path(c.Url, link), buf)
	if err != nil {
		return err
	}
	r := ResourceRequest(link, req)
	if err = r.DefaultHeaders(c.Config.MasterKey); err != nil {
		return err
	}
	r.QueryHeaders(buf.Len())
	return c.do(r, http.StatusOK, ret)
}

// Create resource
func (c *Client) Create(link string, body, ret interface{}) error {
	data, err := stringify(body)
	if err != nil {
		return err
	}
	buf := bytes.NewBuffer(data)
	return c.method("POST", link, http.StatusCreated, ret, buf, nil)
}

// Replace resource
func (c *Client) Replace(link string, body, ret interface{}) error {
	data, err := stringify(body)
	if err != nil {
		return err
	}
	buf := bytes.NewBuffer(data)
	return c.method("PUT", link, http.StatusOK, ret, buf, nil)
}

// ReplaceAsync resource
func (c *Client) ReplaceAsync(link string, body, ret interface{}) error {
	data, err := stringify(body)
	if err != nil {
		return err
	}
	buf := bytes.NewBuffer(data)
	var async *AsyncCall
	if resource, ok := body.(Resource); ok {
		async = &AsyncCall{Etag: resource.Etag}
	}
	return c.method("PUT", link, http.StatusOK, ret, buf, async)
}

// Replace resource
func (c *Client) Execute(link string, body, ret interface{}) error {
	data, err := stringify(body)
	if err != nil {
		return err
	}
	buf := bytes.NewBuffer(data)
	return c.method("POST", link, http.StatusOK, ret, buf, nil)
}

// Private generic method resource
func (c *Client) method(method, link string, status int, ret interface{}, body *bytes.Buffer, async *AsyncCall) (err error) {
	req, err := http.NewRequest(method, path(c.Url, link), body)
	if err != nil {
		return err
	}
	r := ResourceRequest(link, req)
	if err = r.DefaultHeaders(c.Config.MasterKey); err != nil {
		return err
	}
	if async != nil {
		r.AsyncHeaders(async.Etag)
	}
	return c.do(r, status, ret)
}

// Private Do function, DRY
func (c *Client) do(r *Request, status int, data interface{}) error {
	if filepath.Base(r.URL.Path) == "colls" && r.Method == "POST" {
		r.ThroughputHeaders()
	}
	if c.Config.Debug {
		r.QueryMetricsHeaders()
		c.Logger.Infof("CosmosDB Request: ID: %+v, Type: %+v, HTTP Request: %+v", r.rId, r.rType, r.Request)
		curl, _ := http2curl.GetCurlCommand(r.Request)
		c.Logger.Infof("CURL: %s", curl)
	}
	resp, err := c.Do(r.Request)
	if c.Config.Debug && c.Config.Verbose {
		c.Logger.Infof("CosmosDB Request: %s", spew.Sdump(resp.Request))
		c.Logger.Infof("CosmosDB Response Headers: %s", spew.Sdump(resp.Header))
		c.Logger.Infof("CosmosDB Response Content-Length: %s", spew.Sdump(resp.Header))
	}
	if err != nil {
		return fmt.Errorf("Request: Id: %+v, Type: %+v, HTTP: %+v, Error: %s", r.rId, r.rType, r.Request, err)
	}
	if resp.StatusCode != status {
		err = &RequestError{}
		readJson(resp.Body, &err)
		return fmt.Errorf("Request: Id: %+v, Type: %+v, HTTP: %+v, Error: %s", r.rId, r.rType, r.Request, err)
	}
	defer resp.Body.Close()
	if data == nil {
		return nil
	}
	err = readJson(resp.Body, data)
	if err != nil {
		return err
	}
	if c.Config.Debug && c.Config.Verbose {
		c.Logger.Infof("CosmosDB Request: %s", spew.Sdump(resp.Request))
		c.Logger.Infof("CosmosDB Response Headers: %s", spew.Sdump(resp.Header))
		c.Logger.Infof("CosmosDB Response Content-Length: %s", spew.Sdump(resp.Header))
		c.Logger.Infof("CosmosDB Response Content: %s", spew.Sdump(data))
	}
	return nil
}

// Generate link
func path(url string, args ...string) (link string) {
	args = append([]string{url}, args...)
	link = strings.Join(args, "/")
	return
}

// Read json response to given interface(struct, map, ..)
func readJson(reader io.Reader, data interface{}) error {
	return json.NewDecoder(reader).Decode(&data)
}

// Stringify query-string as CosmosDB expected
func querify(query string) string {
	return fmt.Sprintf(`{ "%s": "%s" }`, "query", query)
}

// Stringify body data
func stringify(body interface{}) (bt []byte, err error) {
	switch t := body.(type) {
	case string:
		bt = []byte(t)
	case []byte:
		bt = t
	default:
		bt, err = json.Marshal(t)
	}
	return
}
