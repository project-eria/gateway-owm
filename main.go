package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"runtime"

	configmanager "github.com/project-eria/config-manager"
	"github.com/project-eria/logger"
	"github.com/project-eria/xaal-go/device"
	"github.com/project-eria/xaal-go/engine"
	"github.com/project-eria/xaal-go/schemas"
	"github.com/project-eria/xaal-go/utils"

	owm "github.com/briandowns/openweathermap"
)

func version() string {
	return fmt.Sprintf("0.0.2 - %s (engine commit %s)", engine.Timestamp, engine.GitCommit)
}

const configFile = "gateway-owm.json"

func setupDev(dev *device.Device) {
	dev.VendorID = "ERIA"
	dev.ProductID = "OpenWeatherMap"
	dev.Info = "gateway.owm@OpenWeatherMap"
	dev.URL = "https://www.openweathermap.org"
	dev.Version = version()
}

var config = struct {
	Lang    string `default:"fr"`
	Unit    string `default:"C"`
	Rate    int    `default:"300"`
	APIKey  string `required:"true"`
	Place   string `required:"true"`
	Devices map[string]string
}{}

var _devs []*device.Device
var _weather *owm.CurrentWeatherData

func main() {
	defer os.Exit(0)
	_showVersion := flag.Bool("v", false, "Display the version")
	if !flag.Parsed() {
		flag.Parse()
	}

	// Show version (-v)
	if *_showVersion {
		fmt.Println(version())
		os.Exit(0)
	}

	logger.Module("main").Infof("Starting Gateway OWM %s...", version())

	// Loading config
	cm, err := configmanager.Init(configFile, &config)
	if err != nil {
		if configmanager.IsFileMissing(err) {
			err = cm.Save()
			if err != nil {
				logger.Module("main").WithField("filename", configFile).Fatal(err)
			}
			logger.Module("main").Fatal("JSON Config file do not exists, created...")
		} else {
			logger.Module("main").WithField("filename", configFile).Fatal(err)
		}
	}

	if err := cm.Load(); err != nil {
		logger.Module("main").Fatal(err)
	}
	defer cm.Close()

	engine.Init()

	setup()
	// Save for new Address during setup
	cm.Save()

	// Launch the xAAL engine
	go engine.Run()
	defer engine.Stop()

	// Set up channel on which to send signal notifications.
	// We must use a buffered channel or risk missing the signal
	// if we're not ready to receive when the signal is sent.
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)

	// Block until keyboard interrupt is received.
	<-c
	runtime.Goexit()
}

// setup : create devices, register ...
func setup() {
	// devices
	_devs = append(_devs, schemas.Thermometer(getConfigAddr("temperature")))
	_devs = append(_devs, schemas.Hygrometer(getConfigAddr("humidity")))
	_devs = append(_devs, schemas.Barometer(getConfigAddr("pressure")))

	wind := schemas.Windgauge(getConfigAddr("wind"))
	wind.AddUnsupportedAttribute("gustAngle")
	wind.AddUnsupportedAttribute("gustStrength")
	_devs = append(_devs, wind)

	// gw
	gw := schemas.Gateway(getConfigAddr("addr"))

	var addresses []string
	for _, dev := range _devs {
		addresses = append(addresses, dev.Address)
		setupDev(dev)
		engine.AddDevice(dev)
	}
	gw.SetAttributeValue("embedded", addresses)
	setupDev(gw)
	engine.AddDevice(gw)

	// OWM stuff
	engine.AddTimer(update, config.Rate, -1)

	// We are ready
	var err error
	_weather, err = owm.NewCurrent(config.Unit, config.Lang, config.APIKey)
	if err != nil {
		logger.Module("main").WithError(err).Fatal("Error on OWM init")
	}
}

// getConfigAddr : return a new xaal address and flag an update
func getConfigAddr(key string) string {
	if config.Devices[key] == "" {
		config.Devices[key] = utils.GetRandomUUID()
		logger.Module("main").WithField("addr", config.Devices[key]).Info("New device")
	}
	return config.Devices[key]
}

func update() {
	_weather.CurrentByName(config.Place)
	_devs[0].SetAttributeValue("temperature", _weather.Main.Temp) // TODO Round to 1 decimal
	engine.NotifyAttributesChange(_devs[0])
	_devs[1].SetAttributeValue("humidity", _weather.Main.Humidity)
	engine.NotifyAttributesChange(_devs[1])
	_devs[2].SetAttributeValue("pressure", _weather.Main.Pressure)
	engine.NotifyAttributesChange(_devs[2])

	// TODO Metric: meter/sec, Imperial: miles/hour.
	wind := _weather.Wind.Speed * 3600 / 1000        // m/s => km/h
	_devs[3].SetAttributeValue("windStrength", wind) // TODO Round to 1 decimal
	_devs[3].SetAttributeValue("windAngle", _weather.Wind.Deg)
	engine.NotifyAttributesChange(_devs[3])
	logger.Module("main").WithFields(logger.Fields{
		"temperature":  _weather.Main.Temp,
		"humidity":     _weather.Main.Humidity,
		"pressure":     _weather.Main.Pressure,
		"windStrength": wind,
		"windAngle":    _weather.Wind.Deg,
	}).Trace("Received OWM update")
}
