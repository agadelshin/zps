package zpm

import (
	"errors"
	"strings"

	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"

	"net/url"

	"github.com/chuckpreslar/emission"
	"github.com/nightlyone/lockfile"
	"github.com/solvent-io/zps/config"
	"github.com/solvent-io/zps/zps"
)

type Manager struct {
	*emission.Emitter
	config *config.ZpsConfig
	db     *Db
	cache  *Cache
	lock   lockfile.Lockfile
}

func NewManager(image string) (*Manager, error) {
	var err error
	mgr := &Manager{}

	mgr.Emitter = emission.NewEmitter()

	mgr.config, err = config.LoadConfig(image)
	if err != nil {
		return nil, err
	}

	mgr.lock, err = lockfile.New(filepath.Join(mgr.config.LockPath(), "zpm.lock"))
	if err != nil {
		return nil, err
	}

	mgr.db = &Db{mgr.config.DbPath()}
	mgr.cache = NewCache(mgr.config.CachePath())

	return mgr, nil
}

// TODO: add support for installing from file and repo in one request
func (m *Manager) Install(args []string) error {
	err := m.lock.TryLock()
	if err != nil {
		return errors.New("zpm: locked by another process")
	}
	defer m.lock.Unlock()

	pool, err := m.pool()
	if err != nil {
		return err
	}

	request := zps.NewRequest()
	for _, arg := range args {
		req, err := zps.NewRequirementFromSimpleString(arg)
		if err != nil {
			return err
		}

		if len(pool.WhatProvides(req)) == 0 {
			return errors.New(fmt.Sprint("No candidates found for ", arg))
		}

		request.Install(req)
	}

	// TODO: configure policy
	solver := zps.NewSolver(pool, zps.NewPolicy("updated"))

	solution, err := solver.Solve(request)
	if err != nil {
		return err
	}

	for _, op := range solution.Operations() {
		switch op.Operation {
		case "install":
			uri, _ := url.ParseRequestURI(pool.Location(op.Package.Location()).Uri)
			fe := NewFetcher(uri, m.cache)
			err = fe.Fetch(op.Package.(*zps.Pkg))
			if err != nil {
				return err
			}

			m.Emitter.Emit("fetch", fmt.Sprint(op.Package.Id()))
		case "noop":
			m.Emit("noop", fmt.Sprint("-> using ", op.Package.Id()))
		}
	}

	if solution.Noop() {
		return nil
	}

	tr := NewTransaction(m.config.CurrentImage.Path, m.config.CachePath(), m.db)

	tr.On("install", func(msg string) {
		m.Emit("install", msg)
	})

	tr.On("remove", func(msg string) {
		m.Emit("remove", msg)
	})

	err = tr.Realize(solution)

	return err
}

func (m *Manager) List() ([]string, error) {
	err := m.lock.TryLock()
	if err != nil {
		return nil, errors.New("zpm: locked by another process")
	}
	defer m.lock.Unlock()

	packages, err := m.db.Packages()
	if err != nil {
		return nil, err
	}

	var output []string
	for _, manifest := range packages {
		pkg, _ := zps.NewPkgFromManifest(manifest)

		output = append(output, pkg.Columns())
	}

	if len(packages) == 0 {
		m.Emitter.Emit("warn", "No packages installed.")
		return nil, nil
	}

	return output, nil
}

func (m *Manager) Plan(action string, args []string) (*zps.Solution, error) {
	err := m.lock.TryLock()
	if err != nil {
		return nil, errors.New("zpm: locked by another process")
	}
	defer m.lock.Unlock()

	if action != "install" && action != "remove" {
		return nil, errors.New("action must be either: install or remove")
	}

	pool, err := m.pool()
	if err != nil {
		return nil, err
	}

	request := zps.NewRequest()
	for _, arg := range args {
		req, err := zps.NewRequirementFromSimpleString(arg)
		if err != nil {
			return nil, err
		}

		if len(pool.WhatProvides(req)) == 0 {
			return nil, errors.New(fmt.Sprint("No candidates found for ", arg))
		}

		switch action {
		case "install":
			request.Install(req)
		case "remove":
			request.Remove(req)
		}
	}

	// TODO: configure policy
	solver := zps.NewSolver(pool, zps.NewPolicy("updated"))

	solution, err := solver.Solve(request)
	if err != nil {
		return nil, err
	}

	for _, op := range solution.Operations() {
		switch op.Operation {
		case "noop":
			m.Emitter.Emit("noop", op.Package.Id())
		case "install":
			m.Emitter.Emit("install", op.Package.Id())
		case "remove":
			m.Emitter.Emit("remove", op.Package.Id())
		}

	}

	return solution, nil
}

func (m *Manager) Publish(repo string, pkgs ...string) error {
	for _, r := range m.config.Repos {
		if repo == r.Name && r.Publish.Uri != nil {
			pb := NewPublisher(r.Publish.Uri, r.Publish.Prune)

			err := pb.Publish(pkgs...)

			return err
		}
	}

	return errors.New("Repo: " + repo + " not found")
}

func (m *Manager) Refresh() error {
	err := m.lock.TryLock()
	if err != nil {
		return errors.New("zpm: locked by another process")
	}
	defer m.lock.Unlock()

	for _, r := range m.config.Repos {
		fe := NewFetcher(r.Fetch.Uri, m.cache)

		err := fe.Refresh()
		if err == nil {
			m.Emitter.Emit("refresh", r.Fetch.Uri.String())
		}

		return err
	}

	return nil
}

func (m *Manager) Remove(args []string) error {
	err := m.lock.TryLock()
	if err != nil {
		return errors.New("zpm: locked by another process")
	}
	defer m.lock.Unlock()

	pool, err := m.pool()
	if err != nil {
		return err
	}

	request := zps.NewRequest()
	for _, arg := range args {
		req, err := zps.NewRequirementFromSimpleString(arg)
		if err != nil {
			return err
		}

		if len(pool.WhatProvides(req)) == 0 {
			return errors.New(fmt.Sprint("No removal candidates found for ", arg))
		}

		request.Remove(req)
	}

	// TODO: configure policy
	solver := zps.NewSolver(pool, zps.NewPolicy("updated"))

	solution, err := solver.Solve(request)
	if err != nil {
		return err
	}

	tr := NewTransaction(m.config.CurrentImage.Path, m.config.CachePath(), m.db)

	tr.On("remove", func(msg string) {
		m.Emit("remove", msg)
	})

	err = tr.Realize(solution)

	return err
}

func (m *Manager) RepoInit(name string) error {
	for _, repo := range m.config.Repos {
		if name == repo.Name && repo.Publish.Uri != nil {
			pb := NewPublisher(repo.Publish.Uri, repo.Publish.Prune)

			err := pb.Init()

			return err
		}
	}

	return errors.New("Repo: " + name + " not found")
}

func (m *Manager) RepoContents(name string) ([]string, error) {
	err := m.lock.TryLock()
	if err != nil {
		return nil, errors.New("zpm: locked by another process")
	}
	defer m.lock.Unlock()

	for _, repo := range m.config.Repos {
		if name == repo.Name && repo.Fetch.Uri != nil {

			// TODO fix
			osarch := &zps.OsArch{m.config.CurrentImage.Os, m.config.CurrentImage.Arch}

			packagesfile := m.cache.GetPackages(osarch.String(), repo.Fetch.Uri.String())
			meta := &zps.RepoMeta{}

			pkgsbytes, err := ioutil.ReadFile(packagesfile)

			if err == nil {
				err = meta.Load(pkgsbytes)
				if err != nil {
					return nil, err
				}
			} else if !os.IsNotExist(err) {
				return nil, err
			}

			var contents []string
			for _, pkg := range meta.Repo.Solvables {
				contents = append(contents, strings.Join([]string{pkg.(*zps.Pkg).Name(), pkg.(*zps.Pkg).Uri().String()}, "|"))
			}

			return contents, err
		}
	}

	return nil, errors.New("Repo: " + name + " not found")
}

func (m *Manager) RepoList() ([]string, error) {
	err := m.lock.TryLock()
	if err != nil {
		return nil, errors.New("zpm: locked by another process")
	}
	defer m.lock.Unlock()

	if len(m.config.Repos) == 0 {
		return nil, nil
	}

	var repos []string
	for _, repo := range m.config.Repos {
		repos = append(repos, strings.Join([]string{repo.Name, repo.Fetch.Uri.String()}, "|"))
	}

	return repos, nil
}

func (m *Manager) image() (*zps.Repo, error) {
	packages, err := m.db.Packages()
	if err != nil {
		return nil, err
	}

	var solvables zps.Solvables
	for _, manifest := range packages {
		pkg, _ := zps.NewPkgFromManifest(manifest)

		solvables = append(solvables, pkg)
	}

	image := zps.NewRepo("installed", -1, true, solvables)

	return image, nil
}

func (m *Manager) pool() (*zps.Pool, error) {
	var repos []*zps.Repo

	image, err := m.image()
	if err != nil {
		return nil, err
	}

	for _, r := range m.config.Repos {
		if r.Enabled == true {
			repo := zps.NewRepo(r.Fetch.Uri.String(), r.Priority, r.Enabled, []zps.Solvable{})

			// TODO fix
			osarch := &zps.OsArch{m.config.CurrentImage.Os, m.config.CurrentImage.Arch}

			packagesfile := m.cache.GetPackages(osarch.String(), r.Fetch.Uri.String())
			meta := &zps.RepoMeta{}

			pkgsbytes, err := ioutil.ReadFile(packagesfile)

			if err == nil {
				err = meta.Load(pkgsbytes)
				if err != nil {
					return nil, err
				}
			} else if !os.IsNotExist(err) {
				return nil, err
			}

			for _, pkg := range meta.Repo.Solvables {
				repo.Solvables = append(repo.Solvables, pkg)
			}

			repos = append(repos, repo)
		}
	}

	pool, err := zps.NewPool(image, repos...)
	if err != nil {
		return nil, err
	}

	return pool, nil
}

func (m *Manager) scan(files []string) (*zps.Repo, error) {
	return nil, nil
}
