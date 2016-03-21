// Copyright 2014 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package bind

import (
	"bytes"
	"fmt"
	"go/types"
	"strings"
)

type goGen struct {
	*generator
	imports map[string]struct{}
}

const (
	goPreamble = `// Package gomobile_bind is an autogenerated binder stub for package %[1]s.
//   gobind -lang=go %[2]s
//
// File is generated by gobind. Do not edit.
package gomobile_bind

/*
#include <stdlib.h>
#include <stdint.h>
#include "seq.h"
#include "%[1]s.h"

*/
import "C"

`
)

func (g *goGen) genFuncBody(o *types.Func, selectorLHS string) {
	sig := o.Type().(*types.Signature)
	params := sig.Params()
	for i := 0; i < params.Len(); i++ {
		p := params.At(i)
		pn := "param_" + paramName(params, i)
		g.genRead("_"+pn, pn, p.Type(), modeTransient)
	}

	res := sig.Results()
	if res.Len() > 2 || res.Len() == 2 && !isErrorType(res.At(1).Type()) {
		g.errorf("functions and methods must return either zero or one values, and optionally an error")
		return
	}
	if res.Len() > 0 {
		for i := 0; i < res.Len(); i++ {
			if i > 0 {
				g.Printf(", ")
			}
			g.Printf("res_%d", i)
		}
		g.Printf(" := ")
	}

	g.Printf("%s.%s(", selectorLHS, o.Name())
	for i := 0; i < params.Len(); i++ {
		if i > 0 {
			g.Printf(", ")
		}
		g.Printf("_param_%s", paramName(params, i))
	}
	g.Printf(")\n")

	for i := 0; i < res.Len(); i++ {
		pn := fmt.Sprintf("res_%d", i)
		g.genWrite("_"+pn, pn, res.At(i).Type(), modeRetained)
	}
	if res.Len() > 0 {
		g.Printf("return ")
		for i := 0; i < res.Len(); i++ {
			if i > 0 {
				g.Printf(", ")
			}
			g.Printf("_res_%d", i)
		}
		g.Printf("\n")
	}
}

func (g *goGen) genWrite(toVar, fromVar string, t types.Type, mode varMode) {
	if isErrorType(t) {
		g.Printf("var %s_str string\n", toVar)
		g.Printf("if %s == nil {\n", fromVar)
		g.Printf("    %s_str = \"\"\n", toVar)
		g.Printf("} else {\n")
		g.Printf("    %s_str = %s.Error()\n", toVar, fromVar)
		g.Printf("}\n")
		g.genWrite(toVar, toVar+"_str", types.Typ[types.String], mode)
		return
	}
	switch t := t.(type) {
	case *types.Basic:
		switch t.Kind() {
		case types.String:
			g.Printf("%s := encodeString(%s)\n", toVar, fromVar)
		case types.Bool:
			g.Printf("var %s C.%s = 0\n", toVar, g.cgoType(t))
			g.Printf("if %s { %s = 1 }\n", fromVar, toVar)
		default:
			g.Printf("%s := C.%s(%s)\n", toVar, g.cgoType(t), fromVar)
		}
	case *types.Slice:
		switch e := t.Elem().(type) {
		case *types.Basic:
			switch e.Kind() {
			case types.Uint8: // Byte.
				g.Printf("%s := fromSlice(%s, %v)\n", toVar, fromVar, mode == modeRetained)
			default:
				g.errorf("unsupported type: %s", t)
			}
		default:
			g.errorf("unsupported type: %s", t)
		}
	case *types.Pointer:
		// TODO(crawshaw): test *int
		// TODO(crawshaw): test **Generator
		switch t := t.Elem().(type) {
		case *types.Named:
			g.genToRefNum(toVar, fromVar)
		default:
			g.errorf("unsupported type %s", t)
		}
	case *types.Named:
		switch u := t.Underlying().(type) {
		case *types.Interface, *types.Pointer:
			g.genToRefNum(toVar, fromVar)
		default:
			g.errorf("unsupported, direct named type %s: %s", t, u)
		}
	default:
		g.errorf("unsupported type %s", t)
	}
}

// genToRefNum generates Go code for converting a variable to its refnum.
// Note that the nil-check cannot be lifted into seq.ToRefNum, because a nil
// struct pointer does not convert to a nil interface.
func (g *goGen) genToRefNum(toVar, fromVar string) {
	g.Printf("var %s C.int32_t = _seq.NullRefNum\n", toVar)
	g.Printf("if %s != nil {\n", fromVar)
	g.Printf("	%s = C.int32_t(_seq.ToRefNum(%s))\n", toVar, fromVar)
	g.Printf("}\n")
}

func (g *goGen) genFuncSignature(o *types.Func, objName string) {
	g.Printf("//export proxy%s_%s_%s\n", g.pkgPrefix, objName, o.Name())
	g.Printf("func proxy%s_%s_%s(", g.pkgPrefix, objName, o.Name())
	if objName != "" {
		g.Printf("refnum C.int32_t")
	}
	sig := o.Type().(*types.Signature)
	params := sig.Params()
	for i := 0; i < params.Len(); i++ {
		if objName != "" || i > 0 {
			g.Printf(", ")
		}
		p := params.At(i)
		g.Printf("param_%s C.%s", paramName(params, i), g.cgoType(p.Type()))
	}
	g.Printf(") ")
	res := sig.Results()
	if res.Len() > 0 {
		g.Printf("(")
		for i := 0; i < res.Len(); i++ {
			if i > 0 {
				g.Printf(", ")
			}
			g.Printf("C.%s", g.cgoType(res.At(i).Type()))
		}
		g.Printf(") ")
	}
	g.Printf("{\n")
}

func (g *goGen) genFunc(o *types.Func) {
	if !g.isSigSupported(o.Type()) {
		g.Printf("// skipped function %s with unsupported parameter or result types\n", o.Name())
		return
	}
	g.genFuncSignature(o, "")
	g.Indent()
	g.genFuncBody(o, g.pkgName(g.pkg))
	g.Outdent()
	g.Printf("}\n\n")
}

func (g *goGen) genStruct(obj *types.TypeName, T *types.Struct) {
	fields := exportedFields(T)
	methods := exportedMethodSet(types.NewPointer(obj.Type()))

	for _, f := range fields {
		if t := f.Type(); !g.isSupported(t) {
			g.Printf("// skipped field %s.%s with unsupported type: %T\n\n", obj.Name(), f.Name(), t)
			continue
		}
		g.Printf("//export proxy%s_%s_%s_Set\n", g.pkgPrefix, obj.Name(), f.Name())
		g.Printf("func proxy%s_%s_%s_Set(refnum C.int32_t, v C.%s) {\n", g.pkgPrefix, obj.Name(), f.Name(), g.cgoType(f.Type()))
		g.Indent()
		g.Printf("ref := _seq.FromRefNum(int32(refnum))\n")
		g.genRead("_v", "v", f.Type(), modeRetained)
		g.Printf("ref.Get().(*%s.%s).%s = _v\n", g.pkgName(g.pkg), obj.Name(), f.Name())
		g.Outdent()
		g.Printf("}\n\n")

		g.Printf("//export proxy%s_%s_%s_Get\n", g.pkgPrefix, obj.Name(), f.Name())
		g.Printf("func proxy%s_%s_%s_Get(refnum C.int32_t) C.%s {\n", g.pkgPrefix, obj.Name(), f.Name(), g.cgoType(f.Type()))
		g.Indent()
		g.Printf("ref := _seq.FromRefNum(int32(refnum))\n")
		g.Printf("v := ref.Get().(*%s.%s).%s\n", g.pkgName(g.pkg), obj.Name(), f.Name())
		g.genWrite("_v", "v", f.Type(), modeRetained)
		g.Printf("return _v\n")
		g.Outdent()
		g.Printf("}\n\n")
	}

	for _, m := range methods {
		if !g.isSigSupported(m.Type()) {
			g.Printf("// skipped method %s.%s with unsupported parameter or return types\n\n", obj.Name(), m.Name())
			continue
		}
		g.genFuncSignature(m, obj.Name())
		g.Indent()
		g.Printf("ref := _seq.FromRefNum(int32(refnum))\n")
		g.Printf("v := ref.Get().(*%s.%s)\n", g.pkgName(g.pkg), obj.Name())
		g.genFuncBody(m, "v")
		g.Outdent()
		g.Printf("}\n\n")
	}
}

func (g *goGen) genVar(o *types.Var) {
	if t := o.Type(); !g.isSupported(t) {
		g.Printf("// skipped variable %s with unsupported type %T\n\n", o.Name(), t)
		return
	}
	// TODO(hyangah): non-struct pointer types (*int), struct type.

	v := fmt.Sprintf("%s.%s", g.pkgName(g.pkg), o.Name())

	// var I int
	//
	// func var_setI(v int)
	g.Printf("//export var_set%s_%s\n", g.pkgPrefix, o.Name())
	g.Printf("func var_set%s_%s(v C.%s) {\n", g.pkgPrefix, o.Name(), g.cgoType(o.Type()))
	g.Indent()
	g.genRead("_v", "v", o.Type(), modeRetained)
	g.Printf("%s = _v\n", v)
	g.Outdent()
	g.Printf("}\n")

	// func var_getI() int
	g.Printf("//export var_get%s_%s\n", g.pkgPrefix, o.Name())
	g.Printf("func var_get%s_%s() C.%s {\n", g.pkgPrefix, o.Name(), g.cgoType(o.Type()))
	g.Indent()
	g.Printf("v := %s\n", v)
	g.genWrite("_v", "v", o.Type(), modeRetained)
	g.Printf("return _v\n")
	g.Outdent()
	g.Printf("}\n")
}

func (g *goGen) genInterface(obj *types.TypeName) {
	iface := obj.Type().(*types.Named).Underlying().(*types.Interface)

	summary := makeIfaceSummary(iface)

	// Define the entry points.
	for _, m := range summary.callable {
		if !g.isSigSupported(m.Type()) {
			g.Printf("// skipped method %s.%s with unsupported parameter or return types\n\n", obj.Name(), m.Name())
			continue
		}
		g.genFuncSignature(m, obj.Name())
		g.Indent()
		g.Printf("ref := _seq.FromRefNum(int32(refnum))\n")
		g.Printf("v := ref.Get().(%s.%s)\n", g.pkgName(g.pkg), obj.Name())
		g.genFuncBody(m, "v")
		g.Outdent()
		g.Printf("}\n\n")
	}

	// Define a proxy interface.
	if !summary.implementable {
		// The interface defines an unexported method or a method that
		// uses an unexported type. We cannot generate a proxy object
		// for such a type.
		return
	}
	g.Printf("type proxy%s_%s _seq.Ref\n\n", g.pkgPrefix, obj.Name())

	g.Printf("func (p *proxy%s_%s) Bind_proxy_refnum__() int32 { return p.Bind_Num }\n\n", g.pkgPrefix, obj.Name())

	for _, m := range summary.callable {
		if !g.isSigSupported(m.Type()) {
			g.Printf("// skipped method %s.%s with unsupported parameter or result types\n", obj.Name(), m.Name())
			continue
		}
		sig := m.Type().(*types.Signature)
		params := sig.Params()
		res := sig.Results()

		if res.Len() > 2 ||
			(res.Len() == 2 && !isErrorType(res.At(1).Type())) {
			g.errorf("functions and methods must return either zero or one value, and optionally an error: %s.%s", obj.Name(), m.Name())
			continue
		}

		g.Printf("func (p *proxy%s_%s) %s(", g.pkgPrefix, obj.Name(), m.Name())
		for i := 0; i < params.Len(); i++ {
			if i > 0 {
				g.Printf(", ")
			}
			g.Printf("param_%s %s", paramName(params, i), g.typeString(params.At(i).Type()))
		}
		g.Printf(") ")

		if res.Len() == 1 {
			g.Printf(g.typeString(res.At(0).Type()))
		} else if res.Len() == 2 {
			g.Printf("(%s, error)", g.typeString(res.At(0).Type()))
		}
		g.Printf(" {\n")
		g.Indent()

		for i := 0; i < params.Len(); i++ {
			pn := "param_" + paramName(params, i)
			g.genWrite("_"+pn, pn, params.At(i).Type(), modeTransient)
		}

		if res.Len() > 0 {
			g.Printf("res := ")
		}
		g.Printf("C.cproxy%s_%s_%s(C.int32_t(p.Bind_Num)", g.pkgPrefix, obj.Name(), m.Name())
		for i := 0; i < params.Len(); i++ {
			g.Printf(", _param_%s", paramName(params, i))
		}
		g.Printf(")\n")
		var retName string
		if res.Len() > 0 {
			if res.Len() == 1 {
				T := res.At(0).Type()
				g.genRead("_res", "res", T, modeRetained)
				retName = "_res"
			} else {
				var rvs []string
				for i := 0; i < res.Len(); i++ {
					rv := fmt.Sprintf("res_%d", i)
					g.genRead(rv, fmt.Sprintf("res.r%d", i), res.At(i).Type(), modeRetained)
					rvs = append(rvs, rv)
				}
				retName = strings.Join(rvs, ", ")
			}
			g.Printf("return %s\n", retName)
		}
		g.Outdent()
		g.Printf("}\n\n")
	}
}

func (g *goGen) genRead(toVar, fromVar string, typ types.Type, mode varMode) {
	if isErrorType(typ) {
		g.genRead(toVar+"_str", fromVar, types.Typ[types.String], mode)
		g.Printf("%s := toError(%s_str)\n", toVar, toVar)
		return
	}
	switch t := typ.(type) {
	case *types.Basic:
		switch t.Kind() {
		case types.String:
			g.Printf("%s := decodeString(%s)\n", toVar, fromVar)
		case types.Bool:
			g.Printf("%s := %s != 0\n", toVar, fromVar)
		default:
			g.Printf("%s := %s(%s)\n", toVar, t.Underlying().String(), fromVar)
		}
	case *types.Slice:
		switch e := t.Elem().(type) {
		case *types.Basic:
			switch e.Kind() {
			case types.Uint8: // Byte.
				g.Printf("%s := toSlice(%s, %v)\n", toVar, fromVar, mode == modeRetained)
			default:
				g.errorf("unsupported type: %s", t)
			}
		default:
			g.errorf("unsupported type: %s", t)
		}
	case *types.Pointer:
		switch u := t.Elem().(type) {
		case *types.Named:
			o := u.Obj()
			oPkg := o.Pkg()
			if !g.validPkg(oPkg) {
				g.errorf("type %s is defined in %s, which is not bound", u, oPkg)
				return
			}
			g.Printf("// Must be a Go object\n")
			g.Printf("%s_ref := _seq.FromRefNum(int32(%s))\n", toVar, fromVar)
			g.Printf("%s := %s_ref.Get().(*%s.%s)\n", toVar, toVar, g.pkgName(oPkg), o.Name())
		default:
			g.errorf("unsupported pointer type %s", t)
		}
	case *types.Named:
		switch t.Underlying().(type) {
		case *types.Interface, *types.Pointer:
			hasProxy := true
			if iface, ok := t.Underlying().(*types.Interface); ok {
				hasProxy = makeIfaceSummary(iface).implementable
			}
			o := t.Obj()
			oPkg := o.Pkg()
			if !g.validPkg(oPkg) {
				g.errorf("type %s is defined in %s, which is not bound", t, oPkg)
				return
			}
			g.Printf("var %s %s\n", toVar, g.typeString(t))
			g.Printf("%s_ref := _seq.FromRefNum(int32(%s))\n", toVar, fromVar)
			g.Printf("if %s_ref != nil {\n", toVar)
			g.Printf("	if %s_ref.Bind_Num < 0 { // go object \n", toVar)
			g.Printf("  	 %s = %s_ref.Get().(%s.%s)\n", toVar, toVar, g.pkgName(oPkg), o.Name())
			if hasProxy {
				g.Printf("	} else { // foreign object \n")
				g.Printf("	   %s = (*proxy%s_%s)(%s_ref)\n", toVar, pkgPrefix(oPkg), o.Name(), toVar)
			}
			g.Printf("	}\n")
			g.Printf("}\n")
		default:
			g.errorf("unsupported named type %s", t)
		}
	default:
		g.errorf("unsupported type: %s", typ)
	}
}

func (g *goGen) typeString(typ types.Type) string {
	pkg := g.pkg

	switch t := typ.(type) {
	case *types.Named:
		obj := t.Obj()
		if obj.Pkg() == nil { // e.g. error type is *types.Named.
			return types.TypeString(typ, types.RelativeTo(pkg))
		}
		oPkg := obj.Pkg()
		if !g.validPkg(oPkg) {
			g.errorf("type %s is defined in %s, which is not bound", t, oPkg)
			return "TODO"
		}

		switch t.Underlying().(type) {
		case *types.Interface, *types.Struct:
			return fmt.Sprintf("%s.%s", g.pkgName(oPkg), types.TypeString(typ, types.RelativeTo(oPkg)))
		default:
			g.errorf("unsupported named type %s / %T", t, t)
		}
	case *types.Pointer:
		switch t := t.Elem().(type) {
		case *types.Named:
			return fmt.Sprintf("*%s", g.typeString(t))
		default:
			g.errorf("not yet supported, pointer type %s / %T", t, t)
		}
	default:
		return types.TypeString(typ, types.RelativeTo(pkg))
	}
	return ""
}

// genPreamble generates the preamble. It is generated after everything
// else, where we know which bound packages to import.
func (g *goGen) genPreamble() {
	g.Printf(goPreamble, g.pkg.Name(), g.pkg.Path())
	g.Printf("import (\n")
	g.Indent()
	g.Printf("_seq \"golang.org/x/mobile/bind/seq\"\n")
	for path := range g.imports {
		g.Printf("%q\n", path)
	}
	g.Outdent()
	g.Printf(")\n\n")
}

func (g *goGen) gen() error {
	g.imports = make(map[string]struct{})

	// Switch to a temporary buffer so the preamble can be
	// written last.
	oldBuf := g.printer.buf
	newBuf := new(bytes.Buffer)
	g.printer.buf = newBuf
	g.Printf("// suppress the error if seq ends up unused\n")
	g.Printf("var _ = _seq.FromRefNum\n")

	for _, s := range g.structs {
		g.genStruct(s.obj, s.t)
	}
	for _, intf := range g.interfaces {
		g.genInterface(intf.obj)
	}
	for _, v := range g.vars {
		g.genVar(v)
	}
	for _, f := range g.funcs {
		g.genFunc(f)
	}
	// Switch to the original buffer, write the preamble
	// and append the rest of the file.
	g.printer.buf = oldBuf
	g.genPreamble()
	g.printer.buf.Write(newBuf.Bytes())
	if len(g.err) > 0 {
		return g.err
	}
	return nil
}

// pkgName retuns the package name and adds the package to the list of
// imports.
func (g *goGen) pkgName(pkg *types.Package) string {
	g.imports[pkg.Path()] = struct{}{}
	return pkg.Name()
}
