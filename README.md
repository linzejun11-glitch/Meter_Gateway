# QS300 电表边缘网关

这是一个只接入 QS300 分布式电表的 Go 网关程序。它在现场侧通过串口和
Modbus-RTU 与电表通信，在服务器侧通过 MQTT 与 Python 服务通信。

## 主要功能

- 从 COM3 定时读取 A 相电压、电流和有功功率；
- 读取两路 DI 和两路 DO；
- 接收服务器下发的 DO1、DO2 控制命令；
- 使用功能码 05 写线圈，并轮询地址 310 确认实际状态；
- 使用 MQTT 3.1.1、QoS 0 和设备名/密码认证；
- 使用 12 字节自定义帧头、CRC16-CCITT 和 JSON 属性上报；
- 支持单帧、多帧、选择重传和全部重传；
- 等待 MQTT 连接和 Topic 订阅完成后再开始采集；
- 使用 Context 和 WaitGroup 安全停止任务并关闭串口。

## 项目结构

```text
MOCK_COLLECT/
├─ gateway/
│  └─ cmd/                 正式电表网关入口
├─ meter/
│  ├─ app/                 启动顺序、连接就绪和安全退出
│  ├─ cmd/                 独立电表诊断入口
│  ├─ collector/           周期采集和状态上报
│  ├─ controller/          DO控制、结果确认和ACK缓存
│  ├─ modbus/              QS300 Modbus-RTU及Modbus CRC16
│  └─ transport/           上层使用的统一通信接口
├─ common/
│  ├─ config/              串口、电表、MQTT和采集周期配置
│  ├─ mqttclient/          电表MQTT、JSON、分帧和重传
│  └─ protocol/            电表消息、MQTT帧和CRC16-CCITT
├─ go.mod
├─ go.sum
└─ README.md
```

## 数据链路

```text
QS300电表
  ↕ COM3 / Modbus-RTU
Go电表网关
  ↕ MQTT / 自定义分帧 / JSON
Python MQTT Broker和业务服务
```

## 默认配置

```text
串口：COM3
串口参数：9600 8 N 1
Modbus Slave ID：1
Modbus超时：3秒
采集周期：8秒
MQTT Broker：tcp://192.168.54.150:1883
MQTT设备名：dev1
MQTT密码：123456
属性回复超时：10秒
属性分片大小：30字节
最大重传轮数：3
```

MQTT参数可以通过环境变量覆盖：

```powershell
$env:TRANSPORT="mqtt"
$env:MQTT_BROKER="tcp://192.168.54.150:1883"
$env:MQTT_DEVICE_NAME="dev1"
$env:MQTT_PASSWORD="设备实际密码"
```

## 运行

运行前关闭 SSCOM、ModbusTool 等占用 COM3 的程序。

正式运行：

```powershell
go run ./gateway/cmd
```

单独诊断电表：

```powershell
go run ./meter/cmd
```

两个入口都会使用同一串口和同一 MQTT Client ID，不能同时运行。

## 编译和测试

```powershell
go test ./...
go vet ./...
go build -o ./gateway.exe ./gateway/cmd
go build -o ./meter.exe ./meter/cmd
```

## MQTT Topic

```text
$sys/{device}/post          电表属性上报
$sys/{device}/post/reply    上报确认和重传命令
$sys/{device}/set           DO控制命令
$sys/{device}/set/reply     DO控制结果
```

## DO通道映射

```text
JSON do1 → channel=1 → 仪表DO0 → 端子15/16 → Modbus线圈0
JSON do2 → channel=2 → 仪表DO1 → 端子17/18 → Modbus线圈1
```

控制命令的 id 必须是 13 位数字字符串，每条命令只能控制一路：

```json
{"id":"0000000000001","params":{"do1":true}}
{"id":"0000000000002","params":{"do2":"0"}}
```

DO 值兼容 `true/false`、数字 `0/1` 和字符串 `"0"/"1"`。

## 重要寄存器

```text
70   A相电压
76   A相电流
79   A相有功功率
301  Modbus从站地址
304  波特率编码
305  串口格式编码
310  DO状态
311  DI状态
```

测量单位：

```text
voltage       V
current       mA
active_power  W
```
