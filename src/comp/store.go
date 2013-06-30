// Copyright (c) 2013 Ostap Cherkashin. You can use this source code
// under the terms of the MIT License found in the LICENSE file.

package main

import (
	"bufio"
	"fmt"
	"log"
	"math"
	"os"
	"path"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

type Body chan Value

type Store struct {
	types map[string]ObjectType
	lists map[string]List
}

type Stats struct {
	Total int
	Found int
}

type line struct {
	lineNo  int
	lineStr string
}

var StatsFailed = Stats{-1, -1}

func NewStore() Store {
	return Store{make(map[string]ObjectType), make(map[string]List)}
}

func (s Store) IsDef(name string) bool {
	return s.types[name] != nil
}

func (s Store) Add(fileName string) error {
	name := path.Base(fileName)
	if dot := strings.Index(name, "."); dot > 0 {
		name = name[:dot]
	}

	if !IsIdent(name) {
		return fmt.Errorf("invalid file name: '%v' cannot be used as an identifier (ignoring)", name)
	}

	ot, err := readHead(fileName)
	if err != nil {
		return fmt.Errorf("failed to load %v: %v", fileName, err)
	}

	list, err := readBody(ot, fileName)
	if err != nil {
		return fmt.Errorf("failed to load %v: %v", fileName, err)
	}

	s.types[name] = ot
	s.lists[name] = list

	log.Printf("stored %v (recs %v)", name, len(list))
	return nil
}

func (s Store) Decls() *Decls {
	decls := NewDecls()
	for k, v := range s.lists {
		decls.Declare(k, v, ListType{s.types[k]})
	}

	decls.AddFunc(FuncTrunc())
	decls.AddFunc(FuncDist())
	decls.AddFunc(FuncTrim())
	decls.AddFunc(FuncLower())
	decls.AddFunc(FuncUpper())
	decls.AddFunc(FuncFuzzy())

	return decls
}

func IsIdent(s string) bool {
	ident, _ := regexp.MatchString("^\\w+$", s)
	return ident
}

func readHead(fileName string) (ObjectType, error) {
	file, err := os.Open(fileName)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	buf := bufio.NewReader(file)
	str, err := buf.ReadString('\n')
	if err != nil {
		return nil, err
	}

	fields := strings.Split(str, "\t")
	res := make(ObjectType, len(fields))
	for i, f := range fields {
		f = strings.Trim(f, " \r\n")
		if !IsIdent(f) {
			return nil, fmt.Errorf("invalid field name: '%v'", f)
		}

		res[i].Name = f
		res[i].Type = ScalarType(0)
	}

	return res, nil
}

func readBody(ot ObjectType, fileName string) (List, error) {
	file, err := os.Open(fileName)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	lines := make(chan line, 1024)
	go func() {
		buf := bufio.NewReader(file)

		for lineNo := 0; ; lineNo++ {
			lineStr, _ := buf.ReadString('\n')
			if len(lineStr) == 0 {
				break
			}
			if lineNo == 0 {
				continue
			}

			lines <- line{lineNo, lineStr}
		}
		close(lines)
	}()

	tuples := make(Body, 1024)
	ctl := make(chan int)

	for i := 0; i < runtime.NumCPU(); i++ {
		go tabDelimParser(i, ot, lines, tuples, ctl)
	}
	go func() {
		for i := 0; i < runtime.NumCPU(); i++ {
			<-ctl
		}
		close(tuples)
	}()

	ticker := time.NewTicker(1 * time.Second)
	list := make(List, 0)

	count := 0
	stop := false
	for !stop {
		select {
		case <-ticker.C:
			log.Printf("loading %v (%d tuples)", fileName, count)
		case t, ok := <-tuples:
			if !ok {
				stop = true
				break
			}

			list = append(list, t)
			count++
		}
	}
	ticker.Stop()

	return list, nil
}

func tabDelimParser(id int, ot ObjectType, in chan line, out Body, ctl chan int) {
	count := 0
	for l := range in {
		fields := strings.Split(l.lineStr[:len(l.lineStr)-1], "\t")
		if len(fields) > len(ot) {
			log.Printf("line %d: truncating object (-%d fields)", l.lineNo, len(fields)-len(ot))
			fields = fields[:len(ot)]
		} else if len(fields) < len(ot) {
			log.Printf("line %d: missing fields, appending blank strings", l.lineNo)
			for len(fields) < len(ot) {
				fields = append(fields, "")
			}
		}

		obj := make(Object, len(ot))
		for i, s := range fields {
			num, err := strconv.ParseFloat(s, 64)
			if err != nil || math.IsNaN(num) || math.IsInf(num, 0) {
				obj[i] = String(s)
			} else {
				obj[i] = Number(num)
				count++
			}
		}

		out <- obj
	}

	log.Printf("parser %d found %d numbers\n", id, count)
	ctl <- 1
}
