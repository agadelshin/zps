package zps

import (
	"errors"
	"sort"
)

type Pool struct {
	index map[string]Solvables

	Solvables Solvables

	repos Repos
}

func NewPool(image *Repo, repos ...*Repo) (*Pool, error) {
	pool := &Pool{index: make(map[string]Solvables)}

	if image == nil {
		errors.New("zps.Pool: Image must not be nil, can be empty repository")
	}

	// Force set this for now
	image.Priority = -1
	pool.repos = append(pool.repos, image)

	if len(repos) > 0 {
		for _, repo := range repos {
			pool.repos = append(pool.repos, repo)
		}

		// Sort by priority
		sort.Sort(pool.repos)
	} else {
		return nil, errors.New("zps.Pool: Pool must have at least one repository")
	}

	pool.populate()

	return pool, nil
}

func (p *Pool) Contains(pkg *Pkg) bool {
	if _, ok := p.index[pkg.Name()]; ok {
		for _, candidate := range p.index[pkg.Name()] {
			if candidate.Version().EXQ(pkg.Version()) {
				return true
			}
		}
	}

	return false
}

func (p *Pool) WhatProvides(req *Requirement) Solvables {
	var provides Solvables

	if _, ok := p.index[req.Name]; ok {
		for _, candidate := range p.index[req.Name] {
			if candidate.Satisfies(req) {
				provides = append(provides, candidate)
			}
		}
	}

	return provides
}

func (p *Pool) populate() {
	for index, repo := range p.repos {
		if repo.Enabled == false {
			continue
		}

		for _, solvable := range repo.Solvables {
			solvable.SetPriority(repo.Priority)
			solvable.SetLocation(index)

			p.Solvables = append(p.Solvables, solvable)
			p.index[solvable.Name()] = append(p.index[solvable.Name()], solvable)

			// provides support
			for _, req := range solvable.Requirements() {
				if req.Method == "provides" {
					p.index[req.Name] = append(p.index[req.Name], solvable)
				}
			}
		}
	}

	sort.Sort(p.Solvables)
}
