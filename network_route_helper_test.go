package main

// newTestRouteTable returns an empty RouteTable for use in tests.
func newTestRouteTable() *RouteTable {
	return &RouteTable{entries: make(map[string]*RouteEntry)}
}
