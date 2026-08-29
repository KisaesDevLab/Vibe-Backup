// Package dockerapi is a minimal Docker Engine API client covering exactly
// what the orchestrator needs: enumerate labeled containers, inspect them,
// read files out, stream execs, and run the ephemeral drill container.
//
// It is deliberately not the official SDK — the sidecar image budget is
// 60 MB and the API surface we use is small enough to own. Everything goes
// through DOCKER_HOST, which in production is a filtered socket proxy; the
// endpoint list this package touches is documented in docs/socket-proxy.md
// and must be kept in sync.
package dockerapi

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

// Engine API 1.44 ships with Docker 25 (Jan 2024) and is the oldest
// version current daemons still accept as a client floor.
const apiVersion = "v1.44"

type Client struct {
	http *http.Client
	// host is the URL prefix for requests ("http://docker" for unix
	// sockets, "http://host:port" for tcp).
	host string
	// dial opens a raw connection for hijacked endpoints (exec start).
	dial func(ctx context.Context) (net.Conn, error)
}

func New(dockerHost string) (*Client, error) {
	u, err := url.Parse(dockerHost)
	if err != nil {
		return nil, fmt.Errorf("dockerapi: parse DOCKER_HOST: %w", err)
	}
	var dial func(ctx context.Context) (net.Conn, error)
	host := "http://docker"
	switch u.Scheme {
	case "unix":
		p := u.Path
		dial = func(ctx context.Context) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", p)
		}
	case "tcp", "http":
		addr := u.Host
		host = "http://" + addr
		dial = func(ctx context.Context) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "tcp", addr)
		}
	default:
		return nil, fmt.Errorf("dockerapi: unsupported DOCKER_HOST scheme %q", u.Scheme)
	}
	return &Client{
		host: host,
		dial: dial,
		http: &http.Client{
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					return dial(ctx)
				},
			},
			// No overall timeout: archive and exec streams are long-lived.
		},
	}, nil
}

func (c *Client) url(p string, q url.Values) string {
	s := c.host + "/" + apiVersion + p
	if len(q) > 0 {
		s += "?" + q.Encode()
	}
	return s
}

type apiError struct {
	Status  int
	Message string
}

func (e *apiError) Error() string {
	return fmt.Sprintf("docker API: %d %s", e.Status, e.Message)
}

// IsNotFound reports whether err is a 404 from the daemon.
func IsNotFound(err error) bool {
	ae, ok := err.(*apiError)
	return ok && ae.Status == http.StatusNotFound
}

func (c *Client) do(ctx context.Context, method, p string, q url.Values, body any) (*http.Response, error) {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.url(p, q), rdr)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		defer resp.Body.Close()
		var em struct {
			Message string `json:"message"`
		}
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		if json.Unmarshal(raw, &em) != nil || em.Message == "" {
			em.Message = strings.TrimSpace(string(raw))
		}
		return nil, &apiError{Status: resp.StatusCode, Message: em.Message}
	}
	return resp, nil
}

func (c *Client) doJSON(ctx context.Context, method, p string, q url.Values, body, out any) error {
	resp, err := c.do(ctx, method, p, q, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if out == nil {
		io.Copy(io.Discard, resp.Body)
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// ---- containers -------------------------------------------------------------

type Container struct {
	ID     string            `json:"Id"`
	Names  []string          `json:"Names"`
	Image  string            `json:"Image"`
	Labels map[string]string `json:"Labels"`
	State  string            `json:"State"`
}

// Name returns the primary container name without the leading slash.
func (ct Container) Name() string {
	if len(ct.Names) == 0 {
		return ct.ID[:12]
	}
	return strings.TrimPrefix(ct.Names[0], "/")
}

// ListByLabel returns running containers carrying the given label key.
func (c *Client) ListByLabel(ctx context.Context, label string) ([]Container, error) {
	f, _ := json.Marshal(map[string][]string{"label": {label}})
	q := url.Values{"filters": {string(f)}}
	var out []Container
	err := c.doJSON(ctx, "GET", "/containers/json", q, nil, &out)
	return out, err
}

type ContainerDetail struct {
	ID    string `json:"Id"`
	Name  string `json:"Name"`
	Image string `json:"Image"`
	State struct {
		Running bool `json:"Running"`
	} `json:"State"`
	Config struct {
		Image  string            `json:"Image"`
		Env    []string          `json:"Env"`
		Labels map[string]string `json:"Labels"`
	} `json:"Config"`
	Mounts []struct {
		Type        string `json:"Type"`
		Name        string `json:"Name"`
		Destination string `json:"Destination"`
	} `json:"Mounts"`
	NetworkSettings struct {
		Networks map[string]struct {
			NetworkID string `json:"NetworkID"`
		} `json:"Networks"`
	} `json:"NetworkSettings"`
}

// FirstNetwork returns one network the container is attached to, so an
// ephemeral drill container can be placed where module containers can
// reach it by name.
func (d *ContainerDetail) FirstNetwork() string {
	for name := range d.NetworkSettings.Networks {
		return name
	}
	return ""
}

// EnvValue returns the value of an environment variable set on the
// container, and whether it was present.
func (d *ContainerDetail) EnvValue(name string) (string, bool) {
	prefix := name + "="
	for _, e := range d.Config.Env {
		if strings.HasPrefix(e, prefix) {
			return e[len(prefix):], true
		}
	}
	return "", false
}

// VolumeMountpoint returns the in-container destination of a named volume.
func (d *ContainerDetail) VolumeMountpoint(volume string) (string, bool) {
	for _, m := range d.Mounts {
		if m.Type == "volume" && m.Name == volume {
			return m.Destination, true
		}
	}
	return "", false
}

func (c *Client) Inspect(ctx context.Context, id string) (*ContainerDetail, error) {
	var out ContainerDetail
	err := c.doJSON(ctx, "GET", "/containers/"+id+"/json", nil, nil, &out)
	return &out, err
}

// ---- file access ------------------------------------------------------------

// ReadFile fetches a single file from a container. The archive endpoint
// returns a tar stream; the file must be regular and under maxSize.
func (c *Client) ReadFile(ctx context.Context, id, filePath string, maxSize int64) ([]byte, error) {
	rc, err := c.GetArchive(ctx, id, filePath)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	tr := tar.NewReader(rc)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil, fmt.Errorf("dockerapi: %s not found in archive of %s", filePath, id)
		}
		if err != nil {
			return nil, err
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		if path.Base(hdr.Name) != path.Base(filePath) {
			continue
		}
		if hdr.Size > maxSize {
			return nil, fmt.Errorf("dockerapi: %s is %d bytes, limit %d", filePath, hdr.Size, maxSize)
		}
		return io.ReadAll(io.LimitReader(tr, maxSize))
	}
}

// GetArchive streams a tar archive of a path inside the container.
func (c *Client) GetArchive(ctx context.Context, id, p string) (io.ReadCloser, error) {
	q := url.Values{"path": {p}}
	resp, err := c.do(ctx, "GET", "/containers/"+id+"/archive", q, nil)
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}

// PutArchive extracts a tar stream into a directory inside the container.
func (c *Client) PutArchive(ctx context.Context, id, dir string, tarStream io.Reader) error {
	q := url.Values{"path": {dir}}
	req, err := http.NewRequestWithContext(ctx, "PUT", c.url("/containers/"+id+"/archive", q), tarStream)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-tar")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return &apiError{Status: resp.StatusCode, Message: strings.TrimSpace(string(raw))}
	}
	io.Copy(io.Discard, resp.Body)
	return nil
}

// ---- lifecycle (drill) ------------------------------------------------------

type CreateSpec struct {
	Image      string            `json:"Image"`
	Env        []string          `json:"Env,omitempty"`
	Cmd        []string          `json:"Cmd,omitempty"`
	Labels     map[string]string `json:"Labels,omitempty"`
	HostConfig struct {
		NetworkMode string `json:"NetworkMode,omitempty"`
	} `json:"HostConfig"`
}

// PullImage pulls an image, draining the progress stream.
func (c *Client) PullImage(ctx context.Context, ref string) error {
	q := url.Values{"fromImage": {ref}}
	req, err := http.NewRequestWithContext(ctx, "POST", c.url("/images/create", q), nil)
	if err != nil {
		return err
	}
	// Anonymous auth: module images are public on GHCR.
	req.Header.Set("X-Registry-Auth", base64.URLEncoding.EncodeToString([]byte("{}")))
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return &apiError{Status: resp.StatusCode, Message: strings.TrimSpace(string(raw))}
	}
	// The stream reports errors mid-body; scan for them.
	dec := json.NewDecoder(resp.Body)
	for {
		var msg struct {
			Error string `json:"error"`
		}
		if err := dec.Decode(&msg); err == io.EOF {
			return nil
		} else if err != nil {
			return err
		}
		if msg.Error != "" {
			return fmt.Errorf("dockerapi: pull %s: %s", ref, msg.Error)
		}
	}
}

func (c *Client) CreateContainer(ctx context.Context, name string, spec CreateSpec) (string, error) {
	var out struct {
		ID string `json:"Id"`
	}
	q := url.Values{"name": {name}}
	err := c.doJSON(ctx, "POST", "/containers/create", q, spec, &out)
	return out.ID, err
}

func (c *Client) StartContainer(ctx context.Context, id string) error {
	return c.doJSON(ctx, "POST", "/containers/"+id+"/start", nil, nil, nil)
}

func (c *Client) StopContainer(ctx context.Context, id string, timeout time.Duration) error {
	q := url.Values{"t": {fmt.Sprint(int(timeout.Seconds()))}}
	return c.doJSON(ctx, "POST", "/containers/"+id+"/stop", q, nil, nil)
}

func (c *Client) RemoveContainer(ctx context.Context, id string) error {
	q := url.Values{"force": {"true"}, "v": {"true"}}
	return c.doJSON(ctx, "DELETE", "/containers/"+id, q, nil, nil)
}
