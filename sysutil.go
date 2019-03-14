package sysutil

import (
	"fmt"
	"os"
	"time"
	"math"
	"syscall"
	"os/exec"
	"strings"
	"bufio"
	"strconv"
	"net/http"
	"net"
	"encoding/json"


	"github.com/fatih/color"
	"github.com/NVIDIA/gpu-monitoring-tools/bindings/go/nvml"
	"github.com/mackerelio/go-osstat/memory"
	"github.com/mackerelio/go-osstat/loadavg"
	"github.com/mackerelio/go-osstat/cpu"
	"github.com/go-redis/redis"
)

//Оптимизация функций
//Удаление шаблонов


type Message struct{
	Subscribe []string
	Interval int
	Queue []string
}

type GPUInfo struct {
	FreeMemGPU uint64
	UsedMemGPU uint64
	Temperature uint
	GPU uint
	Encoder uint
	Decoder uint
	Model string
}

type CPUInfo struct {
	FreeMemCPU uint64
	UsedMemCPU uint64
	LoadAVG float64
	Temperature int
}

type DiskStatus struct {
	All  uint64 `json:"all"`
	Used uint64 `json:"used"`
	Free uint64 `json:"free"`
}

type DiskInfo struct {
	Model string
	Path string
	Free float64
	Used float64
	All float64
}

type Info struct{
	GPUInfo []GPUInfo `json:",omitempty"`
	Disk []DiskInfo `json:",omitempty"`
	CPUInfo *CPUInfo `json:",omitempty"`
	Queue *map[string]int64 `json:",omitempty"`
}

const (
	B  = 1
	KB = 1024 * B
	MB = 1024 * KB
	GB = 1024 * MB
)

//************************************************************ 
// 
//					Secondary functions 
//
//************************************************************

func Round(x float64, prec int) float64 {
	var rounder float64
	pow := math.Pow(10, float64(prec))
	intermed := x * pow
	_, frac := math.Modf(intermed)
	if frac >= 0.5 {
		rounder = math.Ceil(intermed)
	} else {
		rounder = math.Floor(intermed)
	}
	return rounder / pow
}


func write_error(err error) {
	red := color.New(color.FgRed).SprintFunc()
	fmt.Printf("\r%s %s \n",err,red("Error"))
	os.Exit(1)
}

func write_ok(message string) {
	green := color.New(color.FgGreen).SprintFunc()
	fmt.Printf("\r%s %s \n",message,green("Done"))
}
//****************************************************
// 
//                      MAIN FUNC
// 
//****************************************************

func CheckTypeSubscribe(r *http.Request,msg []byte) (Message,error){
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
    if err != nil {
        fmt.Printf( "userip: %q is not IP:port", r.RemoteAddr)
    }
    fmt.Printf("\rConnect %s\n" ,ip)
	data := Message{}
	err = json.Unmarshal(msg, &data);
	if err != nil {
        return data,err
    }
  	
  	return  data,nil
}



func checkCUDA() (bool,error){
	nvml.Init()
	defer nvml.Shutdown() 
	count, err := nvml.GetDeviceCount()
	if err != nil {
		return false, err
	}
	var devices []*nvml.Device
	for i := uint(0); i < count; i++ {
		device, err := nvml.NewDevice(i)
		if err != nil {
			return false, err
		}
		devices = append(devices, device)
	}
	return true,nil
}
var redisdb *redis.Client

func checkRedis(addr string) (error){
	redisdb := redis.NewClient(&redis.Options{
		Addr:     addr, 
		Password: "",              
		DB:       0,             
	})

	_, err := redisdb.Ping().Result()
	if err != nil{
		return err
	}
	return err

}

func CheckSystem(redisaddr string) bool{
	err := checkRedis(redisaddr)
	 if err != nil{
	 	write_error(err)
	 }
	write_ok("Check Redis")
	flag,err := checkCUDA()
	if err != nil{
		red := color.New(color.FgRed).SprintFunc()
		fmt.Printf("\r%s %s \n",err,red("Error"))
		fmt.Printf("\r%s","Only CPU")
		return flag
	}
	write_ok("Check CUDA")
	return flag

}

func DiskUsage(path string) (disk DiskStatus) {
	fs := syscall.Statfs_t{}
	err := syscall.Statfs(path, &fs)
	if err != nil {
		return
	}
	disk.All = fs.Blocks * uint64(fs.Bsize)
	disk.Free = fs.Bfree * uint64(fs.Bsize)
	disk.Used = disk.All - disk.Free
	return
}


func getTemp() int {
	file, err := os.Open("/sys/class/thermal/thermal_zone0/temp")
	if err != nil{
		fmt.Printf("\r%s\n",err)
	}
	reader := bufio.NewReader(file)
	temp, err := reader.ReadString('\n')
	if err != nil{
		fmt.Printf("\r%s\n",err)
	}
	temp_val, err := strconv.Atoi(strings.Replace(temp, "\n", "", 1))
	if err != nil{
		fmt.Printf("\r%s\n",err)
	}
	return temp_val/1000
}

func GetDiskINfo() []DiskInfo {
	var diskinfo DiskInfo
	var diskinfo_arr []DiskInfo
	cmd,_ := exec.Command("/bin/sh","-c","lsblk -io TYPE,MODEL,MOUNTPOINT").Output()
	arr:= strings.Split(string(cmd),"\n")
	for _,v := range arr{
		arr_2:= strings.Split(v," ")
		if arr_2[0] == "disk"{
			path :=arr_2[len(arr_2)-1]
			if path == ""{
				path = "/"
			}
			diskinfo.Path=path
			diskinfo.Model=arr_2[1]
			disk := DiskUsage(path)
			diskinfo.All=Round(float64(disk.All)/float64(GB),2)
			diskinfo.Used=Round(float64(disk.Used)/float64(GB),2)
			diskinfo.Free=Round(float64(disk.Free)/float64(GB),2)
			diskinfo_arr=append(diskinfo_arr,diskinfo)
		}
	}
	return diskinfo_arr

}

func GetFreeMemGPU() ( []GPUInfo,error){
	nvml.Init()
	defer nvml.Shutdown() 
	var infogpu GPUInfo
	var infogpu_arr []GPUInfo
	count, err := nvml.GetDeviceCount()
	if err != nil {
		write_error(err)
	}

	var devices []*nvml.Device
	for i := uint(0); i < count; i++ {
		device, err := nvml.NewDevice(i)
		if err != nil {
			fmt.Println(err)
			continue
		}
		devices = append(devices, device)
	}


		for _, device := range devices {
			st, err := device.Status()
			
			if err != nil {
				fmt.Println(err)
				continue
			}
			mem:= (*st).Memory.Global
			infogpu.FreeMemGPU=*mem.Free
			infogpu.UsedMemGPU=*mem.Used
			infogpu.Temperature=*st.Temperature
			infogpu.GPU=*st.Utilization.GPU
			infogpu.Encoder=*st.Utilization.Encoder 
			infogpu.Decoder=*st.Utilization.Decoder
			infogpu.Model=*device.Model
			infogpu_arr=append(infogpu_arr,infogpu)
			return infogpu_arr, nil
			}
	return infogpu_arr, nil
}

func GetCPUInfo() (CPUInfo){
	var cpuinfo CPUInfo
	memory,_ := memory.Get()
	lgv, _ := loadavg.Get()
	cpu, _ := cpu.Get()
	cpuinfo.FreeMemCPU=uint64(memory.Free/MB)
	cpuinfo.UsedMemCPU=uint64(memory.Used/MB)
	cpuinfo.LoadAVG=Round(lgv.Loadavg1/float64(cpu.CPUCount)*100,0)
	cpuinfo.Temperature=getTemp()
	return cpuinfo


}

func GetQueueRedis(q []string,redisdb *redis.Client) map[string]int64{
	queue := make(map[string]int64)
	for _,v := range q{
		result := redisdb.LLen(v)
 		queue[v]=result.Val()
 	}
	return queue
}


func UpdateInfo(i *Info,u int,flag_gpu bool)  {
	for {
		if flag_gpu{	
			i.GPUInfo,_ =GetFreeMemGPU()
		}
		cpuinfo := GetCPUInfo()
		i.CPUInfo = &cpuinfo
		i.Disk = GetDiskINfo()
		time.Sleep(time.Duration(u)*time.Millisecond)
	}
}
func UpdateQueue(i *Info,u time.Duration,q []string,redisdb *redis.Client){
		queue := GetQueueRedis(q,redisdb)
		i.Queue = &queue
		time.Sleep(u*time.Millisecond)
}
