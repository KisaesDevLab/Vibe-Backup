package dockerapi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// ExecResult carries a finished exec's captured output.
type ExecResult struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
}

// Exec runs a command in a container and buffers its output. For large
// streams (pg_dump) use ExecStream instead.
func (c *Client) Exec(ctx context.Context, containerID string, cmd []string, env []string) (*ExecResult, error) {
	var stdout bytes.Buffer
	stderr, code, err := c.ExecStream(ctx, containerID, cmd, env, &stdout)
	if err != nil {
		return nil, err
	}
	return &ExecResult{Stdout: stdout.Bytes(), Stderr: stderr, ExitCode: code}, nil
}

// ExecStream runs a command, writing demultiplexed stdout to w and
// capturing stderr (capped). Returns stderr, the exit code, and any
// transport error. A nonzero exit code is not an error here — callers
// decide what a failure means.
func (c *Client) ExecStream(ctx context.Context, containerID string, cmd, env []string, w io.Writer) (stderr []byte, exitCode int, err error) {
	// 1. Create the exec instance.
	var created struct {
		ID string `json:"Id"`
	}
	err = c.doJSON(ctx, "POST", "/containers/"+containerID+"/exec", nil, map[string]any{
		"AttachStdout": true,
		"AttachStderr": true,
		"Tty":          false,
		"Cmd":          cmd,
		"Env":          env,
	}, &created)
	if err != nil {
		return nil, 0, fmt.Errorf("exec create in %s: %w", containerID, err)
	}

	// 2. Start it over a hijacked connection and demux the raw stream.
	conn, err := c.dial(ctx)
	if err != nil {
		return nil, 0, err
	}
	defer conn.Close()
	if dl, ok := ctx.Deadline(); ok {
		conn.SetDeadline(dl)
	}

	body, _ := json.Marshal(map[string]any{"Detach": false, "Tty": false})
	req := fmt.Sprintf("POST /%s/exec/%s/start HTTP/1.1\r\nHost: docker\r\nContent-Type: application/json\r\nConnection: Upgrade\r\nUpgrade: tcp\r\nContent-Length: %d\r\n\r\n%s",
		apiVersion, created.ID, len(body), body)
	if _, err := conn.Write([]byte(req)); err != nil {
		return nil, 0, err
	}

	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("exec start: %w", err)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols && resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, 0, &apiError{Status: resp.StatusCode, Message: strings.TrimSpace(string(raw))}
	}

	var errBuf bytes.Buffer
	if err := demux(br, w, capWriter(&errBuf, 64<<10)); err != nil {
		return nil, 0, fmt.Errorf("exec stream: %w", err)
	}

	// 3. Collect the exit code.
	var insp struct {
		ExitCode int  `json:"ExitCode"`
		Running  bool `json:"Running"`
	}
	if err := c.doJSON(ctx, "GET", "/exec/"+created.ID+"/json", nil, nil, &insp); err != nil {
		return errBuf.Bytes(), 0, err
	}
	return errBuf.Bytes(), insp.ExitCode, nil
}

// demux splits Docker's multiplexed stream format: each frame is an 8-byte
// header — [stream(1)][pad(3)][big-endian length(4)] — then payload.
func demux(r io.Reader, stdout, stderr io.Writer) error {
	hdr := make([]byte, 8)
	for {
		if _, err := io.ReadFull(r, hdr); err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				return nil
			}
			return err
		}
		n := int64(binary.BigEndian.Uint32(hdr[4:]))
		var dst io.Writer
		switch hdr[0] {
		case 2:
			dst = stderr
		default:
			dst = stdout
		}
		if _, err := io.CopyN(dst, r, n); err != nil {
			return err
		}
	}
}

type cappedWriter struct {
	buf *bytes.Buffer
	max int
}

func capWriter(buf *bytes.Buffer, max int) io.Writer {
	return &cappedWriter{buf: buf, max: max}
}

func (cw *cappedWriter) Write(p []byte) (int, error) {
	if cw.buf.Len() < cw.max {
		room := cw.max - cw.buf.Len()
		if len(p) > room {
			cw.buf.Write(p[:room])
		} else {
			cw.buf.Write(p)
		}
	}
	return len(p), nil
}
