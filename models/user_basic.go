package models

import (
	"fmt"
	"ginchat/utils"
	"time"

	"gorm.io/gorm"
)

// gorm:"column:login_out_time" 是 Gorm 標籤，用於指定該欄位在數據庫中的列名為 login_out_time。這表示在數據庫表中，這個欄位的名稱將為 login_out_time。
// json:"login_out_time" 是 JSON 標籤，它指定在將這個欄位轉換為 JSON 格式時，使用 login_out_time 作為 JSON 物件中的鍵（key）。
type UserBasic struct {
	gorm.Model
	Name          string
	Password      string
	Identity      string
	Phone         string `valid:"matches(^1[3-9]{1}\\d{9}$)~請輸入正確的手機號碼"`
	Email         string `valid:"email~請輸入正確的郵箱格式"`
	ClientIp      string
	ClientPort    string
	LoginTime     time.Time
	HeartbeatTime time.Time
	LoginOutTime  time.Time `gorm:"column:login_out_time" json:"login_out_time"`
	IsLogout      bool
	DeviceInfo    string
}

func (table *UserBasic) TableName() string {
	return "user_basic"
}

func GetUserList() []*UserBasic {
	data := make([]*UserBasic, 10)
	utils.DB.Find(&data) //&data 傳地址，才能夠修改切片。
	for _, v := range data {
		fmt.Println(v)
	}
	return data
}

func CreateUser(user UserBasic) *gorm.DB {
	return utils.DB.Create(&user)
}

func DeleteUser(user UserBasic) *gorm.DB {
	return utils.DB.Delete(&user)
}

func UpdateUser(user UserBasic) *gorm.DB {
	return utils.DB.Model(&user).Updates(UserBasic{
		Name:     user.Name,
		Password: user.Password,
		Phone:    user.Phone,
		Email:    user.Email,
	}) //&user 傳地址，才能夠修改
}
