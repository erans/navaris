package agent

import (
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"syscall"
	"unsafe"

	"github.com/navaris/navaris/internal/provider/firecracker/vsock"
)

// ptyFile wraps a PTY master file and the child process attached to it.
type ptyFile struct {
	master *os.File
	cmd    *exec.Cmd
}

// Read implements io.Reader by reading from the PTY master.
func (p *ptyFile) Read(buf []byte) (int, error) {
	return p.master.Read(buf)
}

// Write implements io.Writer by writing to the PTY master.
func (p *ptyFile) Write(buf []byte) (int, error) {
	return p.master.Write(buf)
}

// Close closes the PTY master and kills the child process.
func (p *ptyFile) Close() error {
	err := p.master.Close()
	if p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
	}
	return err
}

// Resize sets the PTY window size using the TIOCSWINSZ ioctl.
func (p *ptyFile) Resize(w, h int) error {
	ws := struct {
		Row    uint16
		Col    uint16
		Xpixel uint16
		Ypixel uint16
	}{
		Row: uint16(h),
		Col: uint16(w),
	}
	_, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL,
		p.master.Fd(),
		syscall.TIOCSWINSZ,
		uintptr(unsafe.Pointer(&ws)),
	)
	if errno != 0 {
		return errno
	}
	return nil
}

// itoa converts an integer to its decimal string representation without
// importing fmt.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	var buf [20]byte
	pos := len(buf)
	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}

// openPTY opens /dev/ptmx and returns the master fd along with the slave
// device path. It uses TIOCGPTN to find the slave number and TIOCSPTLCK
// to unlock it.
func openPTY() (*os.File, string, error) {
	// Open the master multiplexer.
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		return nil, "", err
	}

	// Retrieve the slave PTY number.
	var n uint32
	_, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL,
		master.Fd(),
		syscall.TIOCGPTN,
		uintptr(unsafe.Pointer(&n)),
	)
	if errno != 0 {
		master.Close()
		return nil, "", errno
	}

	// Unlock the slave PTY (set lock to 0).
	lock := int32(0)
	_, _, errno = syscall.Syscall(
		syscall.SYS_IOCTL,
		master.Fd(),
		syscall.TIOCSPTLCK,
		uintptr(unsafe.Pointer(&lock)),
	)
	if errno != 0 {
		master.Close()
		return nil, "", errno
	}

	slavePath := "/dev/pts/" + itoa(int(n))
	return master, slavePath, nil
}

// allocPTY opens a master/slave PTY pair and starts the given shell inside it.
func allocPTY(shell string) (*ptyFile, error) {
	master, slavePath, err := openPTY()
	if err != nil {
		return nil, err
	}

	// Open the slave end of the PTY.
	slave, err := os.OpenFile(slavePath, os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		master.Close()
		return nil, err
	}
	defer slave.Close()

	cmd := exec.Command(shell)
	cmd.Stdin = slave
	cmd.Stdout = slave
	cmd.Stderr = slave
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid:  true,
		Setctty: true,
		Ctty:    0, // fd index in child's file descriptors (stdin = 0)
	}

	if err := cmd.Start(); err != nil {
		master.Close()
		return nil, err
	}

	return &ptyFile{master: master, cmd: cmd}, nil
}

// HandleSession allocates a PTY, spawns a shell, then bridges the vsock
// connection to the PTY with full stdin/stdout streaming and resize support.
func HandleSession(req *vsock.Message, send SendFunc, conn net.Conn) {
	var payload vsock.SessionPayload
	if err := json.Unmarshal(req.Payload, &payload); err != nil {
		sendExit(send, req.ID, -1)
		return
	}

	shell := payload.Shell
	if shell == "" {
		shell = "/bin/sh"
	}

	pty, err := allocPTY(shell)
	if err != nil {
		sendExit(send, req.ID, -1)
		return
	}

	// done is closed when the PTY output goroutine finishes.
	done := make(chan struct{})

	// Goroutine: read PTY output and forward it as TypeStdout messages.
	go func() {
		defer close(done)
		streamOutput(req.ID, vsock.TypeStdout, pty, send)
	}()

	// Main loop: read messages from the shared connection, skip those not
	// belonging to this session's correlation ID.
	for {
		msg, err := vsock.Decode(conn)
		if err != nil {
			break
		}

		// Skip messages that belong to a different correlation ID.
		if msg.ID != req.ID {
			continue
		}

		switch msg.Type {
		case vsock.TypeStdin:
			var dp vsock.DataPayload
			if err := json.Unmarshal(msg.Payload, &dp); err != nil {
				continue
			}
			if _, err := pty.Write(dp.Data); err != nil {
				goto cleanup
			}

		case vsock.TypeResize:
			var rp vsock.ResizePayload
			if err := json.Unmarshal(msg.Payload, &rp); err != nil {
				continue
			}
			_ = pty.Resize(rp.Width, rp.Height)

		case vsock.TypeSignal:
			goto cleanup
		}
	}

cleanup:
	pty.Close()
	<-done
	sendExit(send, req.ID, 0)
}
