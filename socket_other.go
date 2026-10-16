//go:build !linux

package tcpbrutal

import (
	"errors"
	"fmt"
)

type platformSocketOptions struct{}

func (platformSocketOptions) congestion(int) (string, error) {
	return "", errors.New("tcp_brutal is supported only on Linux")
}
func (platformSocketOptions) setCongestion(int, string) error {
	return errors.New("tcp_brutal is supported only on Linux")
}
func (platformSocketOptions) setParams(int, []byte) error {
	return errors.New("tcp_brutal is supported only on Linux")
}
func (platformSocketOptions) version(int) (uint32, error) {
	return 0, errors.New("tcp_brutal is supported only on Linux")
}

func platformProbe(uint64, uint32, uint64) error {
	return fmt.Errorf("tcp_brutal is supported only on Linux")
}
