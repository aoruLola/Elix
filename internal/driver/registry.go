package driver

import "fmt"

type Registry struct {
	drivers map[string]Driver
}

func NewRegistry() *Registry {
	return &Registry{
		drivers: map[string]Driver{},
	}
}

func (r *Registry) Register(d Driver) {
	r.drivers[d.Name()] = d
}

func (r *Registry) Get(name string) (Driver, error) {
	d, ok := r.drivers[name]
	if !ok {
		return nil, fmt.Errorf("backend %q is not registered", name)
	}
	return d, nil
}

func (r *Registry) All() []Driver {
	out := make([]Driver, 0, len(r.drivers))
	for _, d := range r.drivers {
		out = append(out, d)
	}
	return out
}
