package conmon

import (
	"github.com/hashicorp/go-hclog"
)

type Config struct {
	// options
}

type ConMon interface {
	Start(*Config) error
	Stop() error
}

func New(logger hclog.Logger) ConMon {
	return &conMon{
		logger: logger,
	}
}

type conMon struct {
	logger hclog.Logger

	// hold an object (server) that actually
	// does all the things
}

func (m *conMon) Start(c *Config) error {
	return nil
}

func (m *conMon) Stop() error {
	return nil
}
