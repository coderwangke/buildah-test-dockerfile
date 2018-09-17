package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const baseSpace = "/root/src"

// Builder is
type Builder struct {
	// 用户提供参数, 通过环境变量传入
	GitCloneURL    string
	GitRef         string
	GitType        string
	Image          string
	ImageTagFormat string
	ImageTag       string
	ExtraImageTag  string
	BuildWorkdir   string
	DockerFilePath string
	BuildArgs      string
	NoCache        bool

	HubUser  string
	HubToken string

	hub           string
	gitCommit     string
	gitTag        string
	gitCommitTime string
	projectName   string
	envs          map[string]string
}

// NewBuilder is
func NewBuilder(envs map[string]string) (*Builder, error) {
	b := &Builder{}

	if envs["GIT_CLONE_URL"] != "" {
		b.GitCloneURL = envs["GIT_CLONE_URL"]
		b.GitRef = envs["GIT_REF"]
		b.GitType = envs["GIT_TYPE"]
	} else {
		return nil, fmt.Errorf("envionment variable GIT_CLONE_URL is required")
	}

	if b.GitRef == "" {
		b.GitRef = "master"
		b.GitType = "branch"
	}

	if envs["IMAGE"] == "" {
		return nil, fmt.Errorf("envionment variable IMAGE is required")
	}

	b.HubUser = envs["HUB_USER"]
	b.HubToken = envs["HUB_TOKEN"]

	if b.HubUser == "" || b.HubToken == "" {
		return nil, fmt.Errorf("envionment variable HUB_USER, HUB_TOKEN are required")
	}

	if strings.Index(envs["IMAGE"], ":") > -1 {
		imageAndTag := strings.Split(envs["IMAGE"], ":")
		b.Image, b.ImageTag = imageAndTag[0], imageAndTag[1]
	} else {
		b.Image = envs["IMAGE"]
	}

	if strings.Index(b.Image, ".") > -1 {
		b.hub = b.Image
	} else {
		b.hub = "docker.io" // default server
	}

	if envs["IMAGE_TAG"] != "" { // 高优先级
		b.ImageTag = envs["IMAGE_TAG"]
	} else {
		b.ImageTag = "latest"
	}

	s := strings.TrimSuffix(strings.TrimSuffix(b.GitCloneURL, "/"), ".git")
	b.projectName = s[strings.LastIndex(s, "/")+1:]

	b.DockerFilePath = envs["DOCKERFILE_PATH"]
	b.BuildArgs = envs["BUILD_ARGS"]

	if strings.ToLower(envs["NO_CACHE"]) == "true" {
		b.NoCache = true
	}
	b.envs = envs

	return b, nil
}

func (b *Builder) run() error {
	if err := os.Chdir(baseSpace); err != nil {
		return fmt.Errorf("Chdir to baseSpace(%s) failed:%v", baseSpace, err)
	}

	if err := b.gitPull(); err != nil {
		return err
	}

	if err := b.gitReset(); err != nil {
		return err
	}

	if err := b.loginRegistry(); err != nil {
		return err
	}

	imageURL := fmt.Sprintf("%s:%s", b.Image, b.ImageTag)
	if err := b.build(imageURL); err != nil {
		return err
	}
	if err := b.push(imageURL); err != nil {
		return err
	}

	return nil
}

func (b *Builder) gitPull() error {
	var command = []string{"git", "clone", "--recurse-submodules", b.GitCloneURL, b.projectName}
	if _, err := (CMD{Command: command}).Run(); err != nil {
		fmt.Println("Clone project failed:", err)
		return err
	}
	fmt.Println("Clone project", b.GitCloneURL, "succeed.")
	return nil
}

func (b *Builder) gitReset() error {
	cwd, _ := os.Getwd()
	var command = []string{"git", "checkout", b.GitRef, "--"}
	if _, err := (CMD{command, filepath.Join(cwd, b.projectName)}).Run(); err != nil {
		fmt.Println("Switch to git ref ", b.GitRef, "failed:", err)
		return err
	}
	fmt.Println("Switch to", b.GitRef, "succeed.")
	return nil
}

//修改为podman
func (b *Builder) loginRegistry() error {
	var command = []string{"docker", "login", b.hub, "--username", b.HubUser, "--password", b.HubToken}
	if _, err := (CMD{Command: command}).Run(); err != nil {
		fmt.Println("docker login failed:", err)
		return err
	}
	fmt.Println("docker login succ.")
	return nil
}

//修改为buildah bud -t docker-name .
func (b *Builder) build(imageURL string) error {
	//var contextDir = filepath.Join(baseSpace, b.projectName, b.BuildWorkdir)
	var dockerfilePath string

	dockerfilePath = b.projectName + b.DockerFilePath

	var command = []string{"buildah", "bud"}
	// var command = []string{"docker", "build", "--pull"}

	if dockerfilePath != "" {
		command = append(command, "-f", dockerfilePath)
	}

	if b.NoCache {
		command = append(command, "--no-cache")
	}

	command = append(command, "-t", imageURL)

	if b.BuildArgs != "" {
		args := map[string]string{}
		err := json.Unmarshal([]byte(b.BuildArgs), &args)
		if err != nil {
			fmt.Println("Unmarshal BUILD_ARG error: ", err)
		} else {
			for k, v := range args {
				if strings.HasPrefix(v, "${") && strings.HasSuffix(v, "}") {
					envKey := v[2 : len(v)-1]
					if envValue, ok := b.envs[envKey]; ok {
						command = append(command, "--build-arg", fmt.Sprintf("%s=%s", k, envValue))
						continue
					}
				}
				command = append(command, "--build-arg", fmt.Sprintf("%s=%s", k, v))
			}
		}
	}

	command = append(command, ".")

	if _, err := (CMD{Command: command}).Run(); err != nil {
		fmt.Println("Run docker build failed:", err)
		return err
	}
	fmt.Println("Run docker build succeed.")
	return nil
}

func (b *Builder) push(imageURL string) error {
	var command = []string{"buildah", "push", imageURL}
	if _, err := (CMD{Command: command}).Run(); err != nil {
		fmt.Println("Run docker push failed:", err)
		return err
	}
	fmt.Println("Run docker push succeed.")
	return nil
}

func ensureDirExists(dir string) (err error) {
	f, err := os.Stat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return os.MkdirAll(dir, os.FileMode(0755))
		}
		return err
	}

	if !f.IsDir() {
		return fmt.Errorf("%s is not dir", dir)
	}

	return nil
}

type CMD struct {
	Command []string // cmd with args
	WorkDir string
}

func (c CMD) Run() (string, error) {
	cmdStr := strings.Join(c.Command, " ")
	fmt.Printf("[%s] Run CMD: %s\n", time.Now().Format("2006-01-02 15:04:05"), cmdStr)

	cmd := exec.Command(c.Command[0], c.Command[1:]...)
	if c.WorkDir != "" {
		cmd.Dir = c.WorkDir
	}

	data, err := cmd.CombinedOutput()
	result := string(data)
	if len(result) > 0 {
		fmt.Println(result)
	}

	return result, err
}
