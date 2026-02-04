package server

import (
	"bufio"
	"encoding/binary"
	"io"
	"log"
	"net"
	"sofadb/internal/engine"
	"time"
)

const (
	CmdPut          = 0x01
	CmdRead         = 0x02
	CmdDelete       = 0x03
	CmdReadKeyRange = 0x04
	CmdBatchPut     = 0x05

	StatusOK       = 0x00
	StatusErr      = 0x01
	StatusNotFound = 0x02
)

type TCPServer struct {
	addr     string
	engine   *engine.Engine
	listener net.Listener
}

func NewTCPServer(addr string, engine *engine.Engine) *TCPServer {
	return &TCPServer{
		addr:   addr,
		engine: engine,
	}
}

func (s *TCPServer) Start() error {
	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return err
	}
	s.listener = ln
	log.Printf("TCP Server listening on %s", s.addr)

	for {
		conn, sErr := ln.Accept()
		if sErr != nil {
			// Check if closed
			select {
			case <-time.After(1 * time.Millisecond):
				// Just a check, could use errors.Is(err, net.ErrClosed) but it's simpler to just log and return if we expect close.
				// However, standard accept loop pattern:
				log.Printf("Accept error (stopping?): %v", sErr)
				return sErr
			}
		}
		go s.handleConn(conn)
	}
}

func (s *TCPServer) handleConn(conn net.Conn) {
	defer conn.Close()
	reader := bufio.NewReader(conn)

	// Reusable buffers per connection?
	// For simplicity, allowed allocs for now.
	// Optimization: Use buffer pool.

	for {
		// Read Command (1 byte)
		cmd, err := reader.ReadByte()
		if err == io.EOF {
			return
		}
		if err != nil {
			log.Printf("Read cmd error: %v", err)
			return
		}

		// Read KeyLen (2 bytes)
		var kLen int16
		if err := binary.Read(reader, binary.LittleEndian, &kLen); err != nil {
			log.Printf("Read kLen error: %v", err)
			return
		}

		// Read Key
		keyBuf := make([]byte, kLen)
		if _, err := io.ReadFull(reader, keyBuf); err != nil {
			log.Printf("Read key error: %v", err)
			return
		}
		key := string(keyBuf)

		// Processing based on Cmd
		switch cmd {
		case CmdPut:
			// Read ValLen (4 bytes)
			var vLen int32
			if err := binary.Read(reader, binary.LittleEndian, &vLen); err != nil {
				return
			}
			// Safety Check
			if vLen < 0 || vLen > 64*1024*1024 { // 64MB limit
				return
			}
			valBuf := make([]byte, vLen)
			if _, err := io.ReadFull(reader, valBuf); err != nil {
				return
			}

			err := s.engine.Put(key, valBuf)
			s.writeResponse(conn, err, nil)

		case CmdRead:
			val, err := s.engine.Read(key)
			s.writeResponse(conn, err, val)

		case CmdDelete:
			err := s.engine.Delete(key)
			s.writeResponse(conn, err, nil)

		case CmdReadKeyRange:
			// Read EndKeyLen (2 bytes)
			var endKLen int16
			if err := binary.Read(reader, binary.LittleEndian, &endKLen); err != nil {
				return
			}
			endKeyBuf := make([]byte, endKLen)
			if _, err := io.ReadFull(reader, endKeyBuf); err != nil {
				return
			}
			endKey := string(endKeyBuf)

			results, err := s.engine.ReadKeyRange(key, endKey)
			s.writeRangeResponse(conn, err, results)

		case CmdBatchPut:
			// "key" here is actually "count" of entries?
			// No, let's redefine the protocol for BatchPut.
			// Re-read kLen as batch count?
			// Let's assume kLen was actually batch count for CmdBatchPut.
			count := int(kLen)
			keys := make([]string, count)
			values := make([][]byte, count)

			for i := 0; i < count; i++ {
				var rKLen int16
				binary.Read(reader, binary.LittleEndian, &rKLen)
				rKeyBuf := make([]byte, rKLen)
				io.ReadFull(reader, rKeyBuf)
				keys[i] = string(rKeyBuf)

				var rVLen int32
				binary.Read(reader, binary.LittleEndian, &rVLen)
				rValBuf := make([]byte, rVLen)
				io.ReadFull(reader, rValBuf)
				values[i] = rValBuf
			}

			err := s.engine.BatchPut(keys, values)
			s.writeResponse(conn, err, nil)

		default:
			return // Protocol violation
		}
	}
}

func (s *TCPServer) writeRangeResponse(w io.Writer, err error, results []struct {
	Key   string
	Value []byte
}) {
	if err != nil {
		w.Write([]byte{StatusErr})
		binary.Write(w, binary.LittleEndian, int32(0))
		return
	}

	w.Write([]byte{StatusOK})
	binary.Write(w, binary.LittleEndian, int32(len(results)))
	for _, res := range results {
		binary.Write(w, binary.LittleEndian, int16(len(res.Key)))
		w.Write([]byte(res.Key))
		binary.Write(w, binary.LittleEndian, int32(len(res.Value)))
		w.Write(res.Value)
	}
}

func (s *TCPServer) writeResponse(w io.Writer, err error, val []byte) {
	// Status (1 byte)
	status := StatusOK
	if err == engine.ErrKeyNotFound {
		status = StatusNotFound
	} else if err != nil {
		status = StatusErr
	}

	w.Write([]byte{byte(status)})

	// If OK and Get, write ValLen + Value
	if status == StatusOK {
		vLen := int32(len(val)) // 0 for Put/Delete
		binary.Write(w, binary.LittleEndian, vLen)
		if vLen > 0 {
			w.Write(val)
		}
	} else {
		// Write 0 length for error/notfound
		binary.Write(w, binary.LittleEndian, int32(0))
	}
}

// Close stops the TCP server.
func (s *TCPServer) Close() error {
	if s.listener != nil {
		return s.listener.Close()
	}
	return nil
}
