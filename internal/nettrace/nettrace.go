// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package nettrace provides internal tracing hooks for net package.
package nettrace

// LookupIPAltResolverKey is a context key for an alternate DNS resolver.
// The value should be a func(ctx context.Context, network, host string) ([]net.IPAddr, error).
type LookupIPAltResolverKey struct{}
