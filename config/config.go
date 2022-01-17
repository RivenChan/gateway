package config

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io/ioutil"
	"sync"
	"time"

	configv1 "github.com/go-kratos/gateway/api/gateway/config/v1"
	"github.com/go-kratos/kratos/v2/log"
	"google.golang.org/protobuf/encoding/protojson"
	"sigs.k8s.io/yaml"
)

var (
	LOG = log.NewHelper(log.With(log.GetLogger(), "source", "config"))
)

type OnChange func()

type ConfigLoader interface {
	Load(context.Context) (*configv1.Gateway, error)
	Watch(OnChange)
	Close()
}

type fileLoader struct {
	confPath         string
	confSHA256       string
	watchCancel      context.CancelFunc
	lock             sync.RWMutex
	onChangeHandlers []OnChange
}

var _jsonOptions = &protojson.UnmarshalOptions{DiscardUnknown: true}

func NewFileLoader(confPath string) (ConfigLoader, error) {
	fl := &fileLoader{
		confPath: confPath,
	}
	if err := fl.initialize(); err != nil {
		return nil, err
	}
	return fl, nil
}

func (f *fileLoader) initialize() error {
	sha256hex, err := f.configSHA256()
	if err != nil {
		return err
	}
	f.confSHA256 = sha256hex
	LOG.Infof("the initial config file sha256: %s", sha256hex)

	watchCtx, cancel := context.WithCancel(context.Background())
	f.watchCancel = cancel
	go f.watchproc(watchCtx)
	return nil
}

func sha256sum(in []byte) string {
	sum := sha256.Sum256(in)
	return hex.EncodeToString(sum[:])
}

func (f *fileLoader) configSHA256() (string, error) {
	configData, err := ioutil.ReadFile(f.confPath)
	if err != nil {
		return "", err
	}
	return sha256sum(configData), nil
}

func (f *fileLoader) Load(_ context.Context) (*configv1.Gateway, error) {
	LOG.Infof("loading config file: %s", f.confPath)

	configData, err := ioutil.ReadFile(f.confPath)
	if err != nil {
		return nil, err
	}

	jsonData, err := yaml.YAMLToJSON(configData)
	if err != nil {
		return nil, err
	}
	out := &configv1.Gateway{}
	if err := _jsonOptions.Unmarshal(jsonData, out); err != nil {
		return nil, err
	}
	return out, nil
}

func (f *fileLoader) Watch(fn OnChange) {
	LOG.Info("add config file change event handler")
	f.lock.Lock()
	defer f.lock.Unlock()
	f.onChangeHandlers = append(f.onChangeHandlers, fn)
}

func (f *fileLoader) executeLoader() {
	LOG.Info("execute config loader")
	f.lock.RLock()
	defer f.lock.RUnlock()
	for _, fn := range f.onChangeHandlers {
		fn()
	}
}

func (f *fileLoader) watchproc(ctx context.Context) {
	LOG.Info("start watch config file")
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Second * 5):
		}
		func() {
			sha256hex, err := f.configSHA256()
			if err != nil {
				LOG.Errorf("watch config file error: %+v", err)
				return
			}
			if sha256hex != f.confSHA256 {
				LOG.Infof("config file changed, reload config, last sha256: %s, new sha256: %s", f.confSHA256, sha256hex)
				f.confSHA256 = sha256hex
				f.executeLoader()
				return
			}
			LOG.Info("config file not changed, latest sha256: ", sha256hex)
		}()

	}
}

func (f *fileLoader) Close() {
	f.watchCancel()
}