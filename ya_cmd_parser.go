package redcon

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strconv"
	"sync"
	"unsafe"
)

const (
	CR   = '\r'
	LF   = '\n'
	CRLF = "\r\n"
)

var bufPool sync.Pool

// getBuf returns a buffer with length size from the buffer pool.
func getBuf(size int) *[]byte {
	x := bufPool.Get()
	if x == nil {
		b := make([]byte, size)
		return &b
	}
	buf := x.(*[]byte)
	if cap(*buf) < size {
		bufPool.Put(x)
		b := make([]byte, size)
		return &b
	}
	*buf = (*buf)[:size]
	return buf
}

// PutBuf returns a buffer to the pool.
func PutBuf(buf *[]byte) {
	bufPool.Put(buf)
}

var bufsPool sync.Pool

// getBuf returns a buffer with length size from the buffer pool.
func getBufs(size int) *[][]byte {
	x := bufsPool.Get()
	if x == nil {
		b := make([][]byte, size)
		return &b
	}
	buf := x.(*[][]byte)
	if cap(*buf) < size {
		bufsPool.Put(x)
		b := make([][]byte, size)
		return &b
	}
	*buf = (*buf)[:size]
	return buf
}

// PutBufs returns a buffer to the pool.
func PutBufs(buf *[][]byte) {
	bufsPool.Put(buf)
}

func skipByte(rd *bufio.Reader, c byte) (err error) {
	var tmp byte
	tmp, err = rd.ReadByte()
	if err != nil {
		return
	}
	if tmp != c {
		err = fmt.Errorf(fmt.Sprintf("Illegal Byte [%d] != [%d]", tmp, c))
	}
	return
}
func readLine(rd *bufio.Reader) (line []byte, err error) {
	line, err = rd.ReadSlice(LF)
	if err == bufio.ErrBufferFull {
		return nil, errors.New("line too long")
	}
	if err != nil {
		return
	}
	i := len(line) - 2
	if i < 0 || line[i] != CR {
		err = errors.New("bad line terminator:" + string(line))
	}
	return line[:i], nil
}

func readInt(rd *bufio.Reader) (int, error) {
	if line, err := readLine(rd); err == nil {
		return strconv.Atoi(*(*string)(unsafe.Pointer(&line)))
	} else {
		return 0, err
	}
}

func skipBytes(rd *bufio.Reader, bs []byte) error {
	for i := range bs {
		if err := skipByte(rd, bs[i]); err != nil {
			return err
		}
	}
	return nil
}

func readMyCommand(rd *bufio.Reader) ([][]byte, error) {
	// Read ( *<number of arguments> CR LF )
	if err := skipByte(rd, '*'); err != nil { // io.EOF
		return nil, err
	}
	// number of arguments
	argCount, err := readInt(rd)
	if err != nil {
		return nil, err
	}
	args := *(getBufs(argCount)) // make([][]byte, argCount)
	for i := 0; i < argCount; i++ {
		// Read ( $<number of bytes of argument 1> CR LF )
		if err := skipByte(rd, '$'); err != nil {
			return nil, err
		}

		var argSize int
		argSize, err = readInt(rd)
		if err != nil {
			return nil, err
		}

		// Read ( <argument data> CR LF )
		args[i] = *(getBuf(argSize)) // make([]byte, argSize)
		_, err = io.ReadFull(rd, args[i])
		if err != nil {
			return nil, err
		}

		err = skipBytes(rd, []byte{CR, LF})
		if err != nil {
			return nil, err
		}
	}
	return args, nil
}
