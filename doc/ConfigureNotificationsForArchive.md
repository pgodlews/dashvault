# Telegram notifications and bot

Dashvault can send you a [Telegram](https://telegram.org) message when archiving starts and finishes, and it runs a bot you can send commands to. Both are optional.

Telegram is the only notification channel. teslausb's Pushover, Gotify, IFTTT, Discord, Slack, Matrix, Signal, ntfy, AWS SNS, webhook and shell-command notifications were removed. If your configuration file still has their settings, they are ignored and can be deleted.

Notifications need the Pi to have internet access while it is on your Wi-Fi. A message is sent only when there are clips to archive. The finish message also carries a line on microSD health, and a one-time warning when the card reaches its wear threshold. If you configure temperature thresholds, Dashvault also sends temperature alerts. It does not send live Sentry, archive failure or low-space alerts.

## 1. Create a bot

1. In Telegram, open a chat with [@BotFather](https://t.me/BotFather) and send `/newbot`.
2. Choose a name and a username for the bot.
3. BotFather replies with a token that looks like `123456789:AAExampleExampleExampleExampleExample`. Keep it private: anyone who has it can control the bot.
4. Open a chat with your new bot and send it any message. A bot cannot message you until you have written to it first.

## 2. Find your chat ID

1. In the web app, open Settings and go to **Notifications — Telegram**.
2. Paste the bot token. If it is already saved, you can leave the field blank.
3. In Telegram, send your bot any message.
4. Press **Find my chat ID**. Dashvault fills in the field if exactly one chat has messaged the bot and the field is empty. If there are several, or the field already has an ID, choose yours with **Use this chat**.

Check the name before saving. The chat ID is the bot's allowlist, and anyone who finds your bot can message it. Nothing is allowed until you press **Save**, and you can still edit the field.

If you prefer, type the ID yourself. The bot only answers the chat IDs you list. To allow more than one person, separate the IDs with commas.

## 3. Finish in the web app

Turn on **Enable bot service**, press **Save**, then press **Send test message**. A test message should arrive within a few seconds. The test can reach Telegram while the bot is off, so notifications and commands only work once that switch is on and saved.

## Set it up with the configuration file instead

Add these to `teslausb_setup_variables.conf` before setup:

```bash
export TELEGRAM_ENABLED=true
export TELEGRAM_BOT_TOKEN=123456789:AAExampleExampleExampleExampleExample
export TELEGRAM_CHAT_ID=123456789
export TELEGRAM_SILENT_NOTIFY=false
```

Enter the token exactly as BotFather gave it, without a `bot` prefix. teslausb expected the prefix; Dashvault adds it itself.

`TELEGRAM_SILENT_NOTIFY=true` delivers messages without a sound.

`TELEGRAM_SEND_CLIPS=true` attaches the latest Sentry clip to the finish message when it is under 50 MB.

## Bot commands

| Command | What it does |
|---|---|
| `/status` | Archive status and how long the Pi has been running |
| `/freespace` | Free space on the dashcam drive and the archive |
| `/sdcard` | microSD health, data written and benchmark history |
| `/sync` | Start archiving now |
| `/lastclip` | Send the most recent clip |
| `/recent <n>` | Send the last few clips |
| `/restart` | Restart the archive service |
| `/help` | List the commands |

Clips you ask for are uploaded to Telegram's servers. Telegram bots can send files of up to 50 MB.

## Privacy

Messages and clips sent through the bot pass through Telegram and are stored by it. Bot chats are not end-to-end encrypted. Do not use the bot if that is not acceptable for your footage.
