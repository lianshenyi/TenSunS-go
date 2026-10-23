// Package model contains the normalized cloud resource structures consumed
// by the Consul sync layer. Vendor adapters must populate these exact fields.
package model

// ECS is the normalized cloud host shape from migration baseline section 7.2.
type ECS struct {
	Name   string            `json:"name"`
	Group  string            `json:"group"`
	OSType string            `json:"ostype"`
	Status string            `json:"status"`
	Region string            `json:"region"`
	IP     string            `json:"ip"`
	CPU    string            `json:"cpu"`
	Mem    string            `json:"mem"`
	Exp    string            `json:"exp"`
	Tags   map[string]string `json:"ecstag,omitempty"`
}

// RDS is the normalized MySQL instance shape.
type RDS struct {
	Name   string `json:"name"`
	Domain string `json:"domain"`
	IP     string `json:"ip"`
	Port   int    `json:"port"`
	Region string `json:"region"`
	Group  string `json:"group"`
	Status string `json:"status"`
	IType  string `json:"itype"`
	Ver    string `json:"ver"`
	Exp    string `json:"exp"`
	CPU    string `json:"cpu"`
	Mem    string `json:"mem"`
	Disk   string `json:"disk"`
	Team   string `json:"team,omitempty"`
}

// Redis is the normalized Redis instance shape.
type Redis struct {
	Name   string `json:"name"`
	Domain string `json:"domain"`
	IP     string `json:"ip"`
	Port   int    `json:"port"`
	Region string `json:"region"`
	Group  string `json:"group"`
	Status string `json:"status"`
	IType  string `json:"itype"`
	Ver    string `json:"ver"`
	Mem    string `json:"mem"`
	Exp    string `json:"exp"`
}
