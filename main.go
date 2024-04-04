package main

import (
	"context"
	"embed"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	"github.com/thep0y/go-logger/log"
	"gopkg.in/yaml.v3"
)

type Config struct {
	Listening string `json:"listening"`
	Redis     struct {
		Addr     string `json:"addr"`
		Password string `json:"password"`
		Db       int    `json:"db"`
		Prefix   string `json:"prefix"`
	} `json:"redis"`
}

var (
	//go:embed src/config.yaml
	DefaultConfig embed.FS

	config      Config
	RedisServer *redis.Client
)

func init() {
	log.ErrorPrefix.File = false

	// 初始化配置
	_, err := os.Stat("config.yaml")
	if os.IsNotExist(err) {
		cf, err := DefaultConfig.ReadFile("src/config.yaml")
		if err != nil {
			log.Error(err)
			panic(err)
		}

		err = ioutil.WriteFile("config.yaml", cf, 0644)
		if err != nil {
			log.Error(err)
			panic(err)
		} else {
			log.Info("已创建配置文件，请修改后重新运行")
			os.Exit(0)
		}
	} else if err != nil {
		log.Error(err)
		panic(err)
	}

	js, err := ioutil.ReadFile("config.yaml")
	if err != nil {
		log.Error(err)
		panic(err)
	}

	err = yaml.Unmarshal(js, &config)
	if err != nil {
		log.Error(err.Error())
		panic(err)
	}

	if config.Redis.Prefix != "" && !strings.HasSuffix(config.Redis.Prefix, ":") {
		config.Redis.Prefix += ":"
	}

	// 创建Redis客户端
	RedisServer = redis.NewClient(&redis.Options{
		Addr:     config.Redis.Addr,
		Password: config.Redis.Password,
		DB:       config.Redis.Db,
	})

	// 测试Redis可用性
	for i := 3; i > 0; i-- {
		_, err = RedisServer.Ping(context.Background()).Result()
		if err != nil {
			log.Errorf("Error.致命错误！Redis连接失败,五秒后重试，剩余%d次", i)
			if i == 0 {
				panic(err.Error())
			} else {
				time.Sleep(time.Second * 5)
				continue
			}
		}
	}
}

func main() {
	gin.SetMode(gin.ReleaseMode)
	server := gin.New()
	server.Use(cors.Default(), gin.Recovery())

	server.GET("/", func(c *gin.Context) {
		jsonpCallback := c.Query("jsonpCallback")
		if jsonpCallback == "" || c.Request.Referer() == "" {
			c.JSON(http.StatusNotFound, gin.H{
				"code":    http.StatusNotFound,
				"message": "请求错误",
			})
			return
		}
		u, err := url.ParseRequestURI(c.Request.Referer())
		if err != nil {
			log.Error(err.Error())
			c.JSON(http.StatusInternalServerError, gin.H{
				"code":    http.StatusInternalServerError,
				"message": "服务器内部错误",
			})
			return
		}
		host := u.Hostname()
		path := u.Path

		var (
			site_uv string         = "0" // 站点总访问人数
			site_pv string         = "0" // 站点访问量
			page_pv string         = "0" // 页面访问量
			wg      sync.WaitGroup       // 协程等待
		)
		wg.Add(3)

		// 计算 site_uv
		go func() {
			defer wg.Done()
			RedisServer.SAdd(context.Background(), config.Redis.Prefix+"site_uv:"+host, c.ClientIP())
			suv, err := RedisServer.SCard(context.Background(), config.Redis.Prefix+"site_uv:"+host).Result()
			if err != nil {
				log.Error(err.Error())
				return
			}
			site_uv = strconv.FormatInt(suv, 10)
		}()

		// 计算 site_pv
		go func() {
			defer wg.Done()
			spv, err := RedisServer.HIncrBy(context.Background(),
				config.Redis.Prefix+"site_pv",
				host, 1).Result()
			if err != nil {
				log.Error(err.Error())
				return
			}
			site_pv = strconv.FormatInt(spv, 10)
		}()

		// 计算 page_pv
		go func() {
			defer wg.Done()
			ppv, err := RedisServer.HIncrBy(context.Background(),
				config.Redis.Prefix+"page_pv:"+host,
				path, 1).Result()
			if err != nil {
				log.Error(err.Error())
				return
			}
			page_pv = strconv.FormatInt(ppv, 10)
		}()

		wg.Wait()
		c.Writer.WriteString(`try{` + jsonpCallback + `({"site_uv":` + site_uv + `,"page_pv":` + page_pv + `,"version":2.4,"site_pv":` + site_pv + `})}catch(e){}`)
	})

	log.Info("服务启动，监听" + config.Listening)
	err := server.Run(config.Listening)
	if err != nil {
		log.Error(err.Error())
		panic(err)
	}
}
