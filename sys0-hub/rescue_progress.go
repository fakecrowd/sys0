package main

import "math/bits"

type rescueProgress struct {
	Active     bool   `json:"active"`
	Module     string `json:"module,omitempty"`
	Downloaded int64  `json:"downloaded"`
	Total      int64  `json:"total"`
	Percent    int    `json:"percent"`
	Completed  int    `json:"completed"`
	Modules    int    `json:"modules"`
}

func normalizeRescueProgress(p rescueProgress) rescueProgress {
	if p.Downloaded < 0 {
		p.Downloaded = 0
	}
	if p.Total < 0 {
		p.Total = 0
	}
	if p.Total > 0 && p.Downloaded > p.Total {
		p.Downloaded = p.Total
	}
	if p.Modules < 0 {
		p.Modules = 0
	}
	if p.Completed < 0 {
		p.Completed = 0
	}
	if p.Modules > 0 && p.Completed > p.Modules {
		p.Completed = p.Modules
	}
	if p.Total > 0 {
		hi, lo := bits.Mul64(uint64(p.Downloaded), 100)
		percent, _ := bits.Div64(hi, lo, uint64(p.Total))
		p.Percent = int(percent)
	} else {
		p.Percent = 0
	}
	if p.Percent < 0 {
		p.Percent = 0
	}
	if p.Percent > 100 {
		p.Percent = 100
	}
	if len(p.Module) > 32 {
		p.Module = p.Module[:32]
	}
	return p
}
