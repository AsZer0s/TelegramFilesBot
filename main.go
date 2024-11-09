package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

const (
	botToken      = ""
	botUsername   = ""
	privateChatID = 0
	cacheFilePath = "file_cache.json"
)

var (
	fileStore = make(map[string]string)
	useProxy  = true
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	log.Println("启动机器人...")

	loadCache()

	var httpClient *http.Client
	if useProxy {
		proxyURL, err := url.Parse("http://127.0.0.1:7890")
		if err != nil {
			log.Fatalf("代理URL解析失败: %v", err)
		}
		httpClient = &http.Client{
			Transport: &http.Transport{
				Proxy: http.ProxyURL(proxyURL),
				DialContext: (&net.Dialer{
					Timeout:   5 * time.Second,
					KeepAlive: 5 * time.Second,
					DualStack: false,
				}).DialContext,
			},
		}
		log.Println("代理模式已启用")
	} else {
		httpClient = http.DefaultClient
		log.Println("代理模式已禁用")
	}

	bot, err := tgbotapi.NewBotAPIWithClient(botToken, tgbotapi.APIEndpoint, httpClient)
	if err != nil {
		log.Fatalf("BotAPI初始化失败: %v", err)
	}

	bot.Debug = false
	log.Printf("成功登录机器人: %s", bot.Self.UserName)

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	updates := bot.GetUpdatesChan(u)

	for update := range updates {
		if update.Message == nil {
			continue
		}

		if update.Message.Command() == "start" && strings.HasPrefix(update.Message.CommandArguments(), "download_") {
			fileName := strings.ReplaceAll(strings.TrimPrefix(update.Message.CommandArguments(), "download_"), "_", " ")
			downloadFile(bot, update.Message.Chat.ID, fileName)
			continue
		}

		switch update.Message.Command() {
		case "start":
			msg := tgbotapi.NewMessage(update.Message.Chat.ID, "欢迎使用ZSNET Bot\n向我发送文件来上传\n使用 /list 查看文件列表\n使用 /delete 删除文件")
			sendMessageWithLog(bot, msg, "欢迎信息发送成功")

		case "list":
			if len(fileStore) == 0 {
				msg := tgbotapi.NewMessage(update.Message.Chat.ID, "没有找到任何文件哦")
				sendMessageWithLog(bot, msg, "发送空文件列表信息")
				continue
			}

			fileList := "文件列表:\n"
			i := 1
			for fileName := range fileStore {
				nameWithoutExt := strings.TrimSuffix(fileName, filepath.Ext(fileName))
				escapedFileName := escapeMarkdownV2(nameWithoutExt)
				downloadLink := fmt.Sprintf("https://t.me/%s?start=download_%s", botUsername, strings.ReplaceAll(escapedFileName, " ", "_"))
				fileList += fmt.Sprintf("%d [%s](%s)\n", i, escapedFileName, escapeMarkdownV2(downloadLink))
				i++
			}
			msg := tgbotapi.NewMessage(update.Message.Chat.ID, fileList)
			msg.ParseMode = "MarkdownV2"
			msg.DisableWebPagePreview = true
			sendMessageWithLog(bot, msg, "文件列表发送成功")

		case "delete":
			if update.Message.ReplyToMessage != nil && update.Message.ReplyToMessage.Document != nil {
				fileName := update.Message.ReplyToMessage.Document.FileName
				deleteFile(bot, update.Message.Chat.ID, fileName)
			} else {
				args := update.Message.CommandArguments()
				if args == "" {
					msg := tgbotapi.NewMessage(update.Message.Chat.ID, "请输入要删除的文件名，或者回复包含文件的消息并使用 /delete")
					sendMessageWithLog(bot, msg, "提醒用户输入文件名或回复文件消息")
					continue
				}
				deleteFile(bot, update.Message.Chat.ID, args)
			}

		default:
			if update.Message.Document != nil {
				fileID := update.Message.Document.FileID
				fileName := update.Message.Document.FileName
				fileStore[fileName] = fileID
				saveCache()
				forward := tgbotapi.NewForward(privateChatID, update.Message.Chat.ID, update.Message.MessageID)
				_, err := bot.Send(forward)
				if err != nil {
					msg := tgbotapi.NewMessage(update.Message.Chat.ID, "文件保存失败")
					sendMessageWithLog(bot, msg, fmt.Sprintf("文件保存失败: %s", fileName))
					continue
				}
				msg := tgbotapi.NewMessage(update.Message.Chat.ID, "文件保存成功")
				sendMessageWithLog(bot, msg, fmt.Sprintf("文件已保存并转发到群组: %s", fileName))
			} else if update.Message.Photo != nil {
				photo := update.Message.Photo[len(update.Message.Photo)-1]
				fileID := photo.FileID
				fileName := fmt.Sprintf("photo_%d.jpg", time.Now().Unix())
				fileStore[fileName] = fileID
				saveCache()
				msg := tgbotapi.NewMessage(update.Message.Chat.ID, "图片已保存")
				sendMessageWithLog(bot, msg, fmt.Sprintf("图片已保存: %s", fileName))
			} else if update.Message.Video != nil {
				fileID := update.Message.Video.FileID
				fileName := fmt.Sprintf("video_%d.mp4", time.Now().Unix())
				fileStore[fileName] = fileID
				saveCache()
				msg := tgbotapi.NewMessage(update.Message.Chat.ID, "视频已保存")
				sendMessageWithLog(bot, msg, fmt.Sprintf("视频已保存: %s", fileName))
			} else {
				msg := tgbotapi.NewMessage(update.Message.Chat.ID, "命令错误")
				sendMessageWithLog(bot, msg, "用户发送了无效命令")
			}
		}
	}
}

func deleteFile(bot *tgbotapi.BotAPI, chatID int64, fileName string) {
	if _, exists := fileStore[fileName]; exists {
		delete(fileStore, fileName)
		saveCache()
		msg := tgbotapi.NewMessage(chatID, fmt.Sprintf("文件已删除: %s", fileName))
		sendMessageWithLog(bot, msg, fmt.Sprintf("文件删除成功: %s", fileName))
	} else {
		msg := tgbotapi.NewMessage(chatID, "未找到指定文件")
		sendMessageWithLog(bot, msg, fmt.Sprintf("文件未找到: %s", fileName))
	}
}

func downloadFile(bot *tgbotapi.BotAPI, chatID int64, fileName string) {
	var fileID string
	var exists bool
	var displayName string
	for key, value := range fileStore {
		if strings.TrimSuffix(key, filepath.Ext(key)) == fileName {
			fileID = value
			exists = true
			displayName = strings.TrimSuffix(key, filepath.Ext(key))
			break
		}
	}

	if !exists {
		msg := tgbotapi.NewMessage(chatID, "未找到文件")
		sendMessageWithLog(bot, msg, fmt.Sprintf("下载请求的文件未找到: %s", fileName))
		return
	}

	if strings.HasPrefix(fileName, "photo_") {
		photo := tgbotapi.NewPhoto(chatID, tgbotapi.FileID(fileID))
		photo.Caption = displayName
		sendPhotoWithLog(bot, photo, fmt.Sprintf("图片已发送给用户: %s", displayName))
	} else if strings.HasPrefix(fileName, "video_") {
		video := tgbotapi.NewVideo(chatID, tgbotapi.FileID(fileID))
		video.Caption = displayName
		sendVideoWithLog(bot, video, fmt.Sprintf("视频已发送给用户: %s", displayName))
	} else {
		doc := tgbotapi.NewDocument(chatID, tgbotapi.FileID(fileID))
		doc.Caption = displayName
		sendDocumentWithLog(bot, doc, fmt.Sprintf("文件已发送给用户: %s", displayName))
	}
}

func sendMessageWithLog(bot *tgbotapi.BotAPI, msg tgbotapi.MessageConfig, logMessage string) {
	_, err := bot.Send(msg)
	if err != nil {
		log.Printf("消息发送失败: %v", err)
	} else {
		log.Println(logMessage)
	}
}

func sendDocumentWithLog(bot *tgbotapi.BotAPI, doc tgbotapi.DocumentConfig, logMessage string) {
	_, err := bot.Send(doc)
	if err != nil {
		log.Printf("文件发送失败: %v", err)
	} else {
		log.Println(logMessage)
	}
}

func sendPhotoWithLog(bot *tgbotapi.BotAPI, photo tgbotapi.PhotoConfig, logMessage string) {
	_, err := bot.Send(photo)
	if err != nil {
		log.Printf("图片发送失败: %v", err)
	} else {
		log.Println(logMessage)
	}
}

func sendVideoWithLog(bot *tgbotapi.BotAPI, video tgbotapi.VideoConfig, logMessage string) {
	_, err := bot.Send(video)
	if err != nil {
		log.Printf("视频发送失败: %v", err)
	} else {
		log.Println(logMessage)
	}
}

func escapeMarkdownV2(text string) string {
	specialChars := "_*[]()~`>#+-=|{}.!"
	for _, char := range specialChars {
		text = strings.ReplaceAll(text, string(char), "\\"+string(char))
	}
	return text
}

func loadCache() {
	data, err := ioutil.ReadFile(cacheFilePath)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("缓存加载失败: %v", err)
		}
		return
	}
	err = json.Unmarshal(data, &fileStore)
	if err != nil {
		log.Printf("解析缓存失败: %v", err)
	} else {
		log.Println("文件缓存已成功加载")
	}
}

func saveCache() {
	data, err := json.Marshal(fileStore)
	if err != nil {
		log.Printf("缓存保存失败: %v", err)
		return
	}
	err = ioutil.WriteFile(cacheFilePath, data, 0644)
	if err != nil {
		log.Printf("缓存写入失败: %v", err)
	} else {
		log.Println("文件缓存已成功保存")
	}
}
