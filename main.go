package main

import (
	"os"

	"github.com/project-eria/eria-base"
	configmanager "github.com/project-eria/eria-base/config-manager"
	"github.com/project-eria/eria-logger"
	"github.com/project-eria/xaal-go"
	"github.com/project-eria/xaal-go/device"
	"github.com/project-eria/xaal-go/schemas"
	"github.com/project-eria/xaal-go/utils"

	owm "github.com/briandowns/openweathermap"
)

var (
	// Version is a placeholder that will receive the git tag version during build time
	Version = "-"
)

func setupDev(dev *device.Device) {
	dev.VendorID = "ERIA"
	dev.ProductID = "OpenWeatherMap"
	dev.Info = "gateway.owm@OpenWeatherMap"
	dev.URL = "https://www.openweathermap.org"
	dev.Version = Version
}

const configFile = "gateway-owm.json"

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

	eria.AddShowVersion(Version)

	logger.Module("main").Infof("Starting Gateway OWM %s...", Version)

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

	// Init xAAL engine
	eria.InitEngine()

	setup()
	// Save for new Address during setup
	cm.Save()

	// Launch the xAAL engine
	go xaal.Run()
	defer xaal.Stop()

	eria.WaitForExit()
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
		xaal.AddDevice(dev)
	}
	gw.SetAttributeValue("embedded", addresses)
	setupDev(gw)
	xaal.AddDevice(gw)

	// OWM stuff
	xaal.AddTimer(update, config.Rate, -1)

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
	xaal.NotifyAttributesChange(_devs[0])
	_devs[1].SetAttributeValue("humidity", _weather.Main.Humidity)
	xaal.NotifyAttributesChange(_devs[1])
	_devs[2].SetAttributeValue("pressure", _weather.Main.Pressure)
	xaal.NotifyAttributesChange(_devs[2])

	// TODO Metric: meter/sec, Imperial: miles/hour.
	wind := _weather.Wind.Speed * 3600 / 1000        // m/s => km/h
	_devs[3].SetAttributeValue("windStrength", wind) // TODO Round to 1 decimal
	_devs[3].SetAttributeValue("windAngle", _weather.Wind.Deg)
	xaal.NotifyAttributesChange(_devs[3])
	logger.Module("main").WithFields(logger.Fields{
		"temperature":  _weather.Main.Temp,
		"humidity":     _weather.Main.Humidity,
		"pressure":     _weather.Main.Pressure,
		"windStrength": wind,
		"windAngle":    _weather.Wind.Deg,
	}).Trace("Received OWM update")
}
