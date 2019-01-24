// Copyright 2015 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package hello is a trivial package for gomobile bind example.
package hello

import (
	"errors"
	"fmt"

	"github.com/btcsuite/btcd/chaincfg"

	"github.com/btcsuite/btcutil/hdkeychain"
)

type Kind = string

func Greetings(name Kind) string {
	return fmt.Sprintf("Hello, %s!", name)
}

func GeneratePrivateKey() (string, error) {
	seed, err := hdkeychain.GenerateSeed(32)
	if err != nil {
		return "", err
	}
	key, err := hdkeychain.NewMaster(seed, &chaincfg.MainNetParams)
	if err != nil {
		return "", err
	}
	return key.String(), nil
}

func ThrowsError() error {
	return errors.New("this is my error")
}

type Foo struct {
	A   int32
	Str string
}

func DoSomethingWithFoo(foo *Foo) *Foo {
	foo.A = 5
	foo.Str = "foo"
	return foo
}

func LaunchGoroutine() string {
	ch := make(chan string)
	go func() {
		for {}
		ch <- "hello"
	}()
	return "<-ch"
}
