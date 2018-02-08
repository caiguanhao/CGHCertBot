## CGHCertBot

A telegram bot.

### Usage

Example:

```sh
# Add this line to /etc/rc.local to run the bot "forever":
BOTAPI=... \
  nohup bash -c 'while true; do /usr/local/bin/cghcertbot --data /botdata.json && break; sleep 3; done' >/root/nohup.txt 2>&1 &

# report everyday at 9am with crontab:
0 9 * * * BOTAPI=... /usr/local/bin/cghcertbot --summarize --data /botdata.json
```

LICENSE: MIT
