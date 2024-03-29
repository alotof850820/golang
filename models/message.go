package models

import (
	"context"
	"encoding/json"
	"fmt"
	"ginchat/utils"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/fatih/set"
	"github.com/go-redis/redis/v8"
	"github.com/gorilla/websocket"
	"github.com/spf13/viper"
	"gorm.io/gorm"
)

// 消息
type Message struct {
	gorm.Model
	UserId     int64  //發送者
	TargetId   int64  //接收者
	Type       int    //消息類型 群聊 私聊 廣播
	Media      int    //消息類型 文字 圖片 音訊
	Content    string //消息內容
	Url        string //文件地址
	Pic        string //圖片地址
	Desc       string //描述
	Amount     int    //其他數字統計
	CreateTime uint64
	ReadTime   uint64 //讀取時間
}

func (table *Message) TableName() string {
	return "message"
}

// WebSocket 连接的节点
type Node struct {
	Conn          *websocket.Conn
	DateQueue     chan []byte   // 用于在节点之间传递数据。 传递数据是字节数组
	GroupSets     set.Interface // 好友/群聊
	Addr          string        //客戶端的地址
	FirstTime     int64         //首次连接时间
	HeartbeatTime uint64        //心跳时间
	LoginTime     uint64        //登录时间
}

// 映射關係 0开始时没有分配内存。
var clientMap map[int64]*Node = make(map[int64]*Node, 0) //make创建并初始化

// 互斥锁 对于读多写少的情况是很有用的。
var rwLocker sync.RWMutex //用于在多个 goroutine 之间保护共享资源的访问。

// need 發送者ID 接收者ID 消息內容 消息類型 媒體類型
func Chat(writer http.ResponseWriter, request *http.Request) {
	// 1. 獲取參數並 check token
	// token := query.Get("token")
	query := request.URL.Query()
	Id := query.Get("userId")
	userId, _ := strconv.ParseInt(Id, 10, 64) //将字符串 Id 转换为 int64 类型的整数
	// targetId := query.Get("targetId")
	// content := query.Get("content")
	// msgType := query.Get("type")
	isvalid := true // 先設定為有效...............
	conn, err := (&websocket.Upgrader{
		// token check
		CheckOrigin: func(r *http.Request) bool { //是否接受跨域连接。
			return isvalid
		},
	}).Upgrade(writer, request, nil) //将 HTTP 连接升级为 WebSocket 连接。
	if err != nil {
		fmt.Println(err)
		return
	}

	// 2. 獲取連接 conn
	node := &Node{
		Conn:      conn,
		DateQueue: make(chan []byte, 50),   //带有 50 个缓冲区的 chan
		GroupSets: set.New(set.ThreadSafe), //创建了线程安全的空集合 用于管理节点所属的群组。
	}

	// 3. 用戶關係
	// 4. userId -> node 綁定並上鎖
	rwLocker.Lock()
	clientMap[userId] = node // userId 是key，将节点与对应的用户串起来。
	rwLocker.Unlock()

	// 5. 完成監聽與發送邏輯
	go sendProc(node)
	// 6. 完成接受發送邏輯
	go recvProc(node)

	sendMsg(userId, []byte("欢迎进入聊天室"))
}

func sendProc(node *Node) {
	for { // 不断监听通道并发送数据。
		select { // 用于监听多个通道的操作。 在这里，只监听 node.DateQueue 这一个通道。
		case data := <-node.DateQueue: // DateQueue有数据可读时，将数据赋值给 data。
			fmt.Println("[ws] send>>>>>>msg:", string(data))
			err := node.Conn.WriteMessage(websocket.TextMessage, data) //发送给客户端。
			if err != nil {
				fmt.Println(err)
				return
			}
		}
	}
}

func recvProc(node *Node) {
	for {
		_, data, err := node.Conn.ReadMessage() // 读取客户端发送的数据
		if err != nil {
			fmt.Println(err)
			return
		}

		broadMsg(data)
		node.DateQueue <- data // 将数据写入通道
		fmt.Println("[ws] recv <<<<<< msg:", string(data))
	}
}

var udpsendChan chan []byte = make(chan []byte, 1024)

// 广播消息
func broadMsg(data []byte) {
	udpsendChan <- data
}

func init() {
	go udpSendProc()
	go udpRecvProc()
	fmt.Println("init go")
}

// 完成udp数据发送协程
func udpSendProc() {
	// 创建 UDP 连接
	con, err := net.DialUDP("udp", nil, &net.UDPAddr{
		IP:   net.IPv4(192, 168, 12, 1),
		Port: viper.GetInt("port.udpport"),
	})

	if err != nil {
		fmt.Println(err)
		return
	}
	defer con.Close()
	// 監聽udpsendChan 通道 发送数据
	for {
		select {
		case data := <-udpsendChan:
			fmt.Println("[ws] udpSendProc data:", string(data))
			// 通过 UDP 连接发送数据到指定的 IP 地址和端口
			_, err := con.Write(data)
			if err != nil {
				fmt.Println(err)
				return
			}
		}
	}
}

// 完成udp数据接收协程
func udpRecvProc() {
	// 创建 UDP 连接
	con, err := net.ListenUDP("udp", &net.UDPAddr{
		IP:   net.IPv4zero,
		Port: viper.GetInt("port.udpport"),
	})

	if err != nil {
		fmt.Println(err)
		return
	}

	defer con.Close()
	for {
		// 从 UDP 连接 con 中读取所有数据，将读取的数据存储到 buf 中。
		var buf [512]byte

		n, err := con.Read(buf[0:])
		if err != nil {
			fmt.Println(err)
			return
		}
		// 参数是实际读取到的数据。
		dispatch(buf[0:n])
	}
}

// 後端調度邏輯處理
func dispatch(data []byte) {
	msg := Message{}
	msg.CreateTime = uint64(time.Now().Unix())
	err := json.Unmarshal(data, &msg) // 接收JSON 的 UDP 数据 data 解析成 msg 变量。
	if err != nil {
		fmt.Println(err)
		return
	}
	switch msg.Type {
	case 1: // 私聊
		fmt.Println("[ws] dispatch data:", string(data))
		sendMsg(msg.TargetId, data)
	case 2: // 群聊
		sendGroupMsg(msg.TargetId, data)
		// case 3: // 广播
		// 	sendAllMsg(msg)
		// case 4:

	}
}

// 确保向用户发送消息时不会受到其他 goroutine 的影响。
func sendMsg(userId int64, msg []byte) {
	fmt.Println("[ws] sendMsg userId:", userId, "msg:", string(msg))
	//通过读取锁确保在查找用户信息时其他 goroutine 不会修改 clientMap。
	rwLocker.RLock() //使用读取锁，多个 goroutine 可同时读取 clientMap 而不会互斥。
	node, ok := clientMap[userId]
	rwLocker.RUnlock()
	jsonMsg := Message{}
	json.Unmarshal(msg, &jsonMsg)
	ctx := context.Background()
	targetIdStr := strconv.Itoa(int(userId))
	userIdStr := strconv.Itoa(int(jsonMsg.UserId))
	jsonMsg.CreateTime = uint64(time.Now().Unix())
	r, err := utils.Red.Get(ctx, "online_"+userIdStr).Result()
	if err != nil {
		fmt.Println(err) // 沒有在redis裡面
	}
	if r != "" {
		if ok {
			fmt.Println("[red] sendMsg userId:", userId, "msg:", string(msg))
			node.DateQueue <- msg
		}
	}

	var key string
	if userId > jsonMsg.UserId {
		key = "msg_" + userIdStr + "_" + targetIdStr
	} else {
		key = "msg_" + targetIdStr + "_" + userIdStr
	}
	// 案時間排序
	res, err := utils.Red.ZRevRange(ctx, key, 0, -1).Result()
	if err != nil {
		fmt.Println(err)
	}
	score := float64(cap(res)) + 1
	response, e := utils.Red.ZAdd(ctx, key, &redis.Z{Score: score, Member: msg}).Result() //jsonMsg
	if e != nil {
		fmt.Println(e)
	}
	fmt.Println(response)
}

// 需要重寫才能將msg轉成byte[]
func (msg Message) MarshalBinary() ([]byte, error) {
	return json.Marshal(msg)
}

// 獲取緩存中的數據
func RedisMsg(userIdA int64, userIdB int64, start int64, end int64, isRev bool) []string {
	rwLocker.RLock()
	// node, ok := clientMap[userIdA]
	rwLocker.RUnlock()
	// jsonMsg := Message{}
	// json.Unmarshal(msg, &jsonMsg)
	ctx := context.Background()
	userIdStr := strconv.Itoa(int(userIdA))
	targetIdStr := strconv.Itoa(int(userIdB))

	var key string

	if userIdA > userIdB {
		key = "msg_" + targetIdStr + "_" + userIdStr
	} else {
		key = "msg_" + userIdStr + "_" + targetIdStr
	}

	var r []string
	var err error
	if isRev {
		r, err = utils.Red.ZRange(ctx, key, start, end).Result()
	} else {
		r, err = utils.Red.ZRevRange(ctx, key, start, end).Result()
	}

	// r, err := utils.Red.ZRevRange(ctx, key, 0, 10).Result()
	if err != nil {
		fmt.Println(err) // 沒有在redis裡面
	}

	// for _, v := range r {
	// 	fmt.Println("[red] sendMsg userId:", userIdA, "msg:", v)
	// 	nodeA.DateQueue <- []byte(v)
	// }
	return r

}

func JoinGroup(userId uint, groupId string) (int, string) {
	contact := Contact{
		OwnerId: userId,
		// TargetId: groupId,
		Type: 2,
	}
	community := Community{}

	utils.DB.Where("id=? or name = ?", groupId, groupId).Find(&community)
	if community.Name == "" {
		return -1, "群不存在"
	}

	utils.DB.Where("owner_id=? and target_id=? and type = 2", userId, groupId).Find(&contact)
	if !contact.CreatedAt.IsZero() {
		return -1, "已經在群組裡了"
	} else {
		contact.TargetId = community.ID
		utils.DB.Create(&contact)
		return 200, "成功加入群組"
	}
}

func sendGroupMsg(groupId int64, msg []byte) {
	//
}

// 更新用戶心跳
func (node *Node) Heartbeat(currentTime uint64) {
	node.HeartbeatTime = currentTime
	return
}

// 清理超時連接
func CleanConnection(param interface{}) (result bool) {
	return true
}
