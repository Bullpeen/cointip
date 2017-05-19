package cointip

import (
	"context"
	"sync"

	"strings"

	"fmt"

	log "github.com/Sirupsen/logrus"
	"github.com/jirwin/quadlek/quadlek"
	"github.com/morgabra/cointip"
)

var coinbaseClient *cointip.ApiKeyClient
var bankAccount *cointip.Account
var accountsCache []*cointip.Account
var accountsCacheLock = &sync.Mutex{}

func help(cmdMsg *quadlek.CommandMsg) {
	cmdMsg.Command.Reply() <- &quadlek.CommandResp{
		Text:      "cointip: Tip your friends!\nAvailable commands: help, balance, deposit, withdraw",
		InChannel: false,
	}
}

func sayError(cmdMsg *quadlek.CommandMsg, msg string, inChannel bool) {
	cmdMsg.Command.Reply() <- &quadlek.CommandResp{
		Text:      fmt.Sprintf("Uh Oh. Something broke: %s", msg),
		InChannel: inChannel,
	}
}

func say(cmdMsg *quadlek.CommandMsg, msg string, inChannel bool) {
	cmdMsg.Command.Reply() <- &quadlek.CommandResp{
		Text:      msg,
		InChannel: inChannel,
	}
}

func accountBalanceString(account *cointip.Account) string {
	return fmt.Sprintf(
		"%s:%.2f %s:%.8f",
		account.NativeBalance.Currency, account.NativeBalance.Amount,
		account.Balance.Currency, account.Balance.Amount,
	)
}

func getOrCreateAccount(userId string, refresh bool) (*cointip.Account, error) {

	acctId := fmt.Sprintf("cointip_%s", userId)

	accountsCacheLock.Lock()
	defer accountsCacheLock.Unlock()

	// Warm the cache
	if len(accountsCache) == 0 {
		accts, err := coinbaseClient.ListAccounts()
		if err != nil {
			return nil, err
		}
		accountsCache = accts
	}

	for i, account := range accountsCache {
		// If we find an account in the cache, we optionally refresh it and return it
		if account.ID == acctId {
			if refresh {
				account, err := coinbaseClient.GetAccount(account.ID)
				if err != nil {
					return nil, err
				}
				accountsCache[i] = account
			}
			return account, nil
		}
	}

	// Otherwise, create and cache it
	account, err := coinbaseClient.CreateAccount(acctId)
	if err != nil {
		return nil, err
	}
	accountsCache = append(accountsCache, account)
	log.Infof("Created new cointip account: %s", acctId)

	tx, err := coinbaseClient.Transfer(bankAccount.ID, account.ID, &cointip.Balance{Currency: cointip.CurrencyUSD, Amount: 3.00})
	if err != nil {
		log.WithError(err).Errorf("Failed to prime new cointip account from bank: %s", bankAccount.ID)
		// non-fatal, so we still return the account
	} else {
		log.Infof("Primed new cointip account %s txid: %s", acctId, tx.ID)
	}

	return account, nil
}

func cointipReaction(ctx context.Context, reactionChannel <-chan *quadlek.ReactionHookMsg) {
	for {
		select {
		case rh := <-reactionChannel:

			amount := &cointip.Balance{
				Currency: cointip.CurrencyUSD,
			}
			switch rh.Reaction.Reaction {
			case ":cointip_1:":
				amount.Amount = .01
			case ":cointip_2:":
				amount.Amount = .02
			case ":cointip_5:":
				amount.Amount = .05
			case ":cointip_10:":
				amount.Amount = .10
			case ":cointip_25:":
				amount.Amount = .25
			default:
				return
			}

			from, err := getOrCreateAccount(rh.Reaction.User, false)
			if err != nil {
				log.WithError(err).Error("Failed fetching coinbase account.")
				return
			}
			to, err := getOrCreateAccount(rh.Reaction.ItemUser, false)
			if err != nil {
				log.WithError(err).Error("Failed fetching coinbase account.")
				return
			}

			tx, err := coinbaseClient.Transfer(from.ID, to.ID, amount)
			if err != nil {
				log.WithError(err).Error("Failed creating transaction.")
				return
			}

			log.Infof("%s tipped %s %s:%.2f txid: %s", from.ID, to.ID, tx.NativeAmount.Currency, tx.NativeAmount.Amount, tx.ID)

		case <-ctx.Done():
			return
		}
	}
}

func cointipCommand(ctx context.Context, cmdChannel <-chan *quadlek.CommandMsg) {
	for {
		select {
		case cmdMsg := <-cmdChannel:

			// /cointip <command> <args...>
			cmd := strings.SplitN(cmdMsg.Command.Text, " ", 1)
			if len(cmd) == 0 {
				help(cmdMsg)
				return
			}

			switch cmd[0] {
			case "balance":
				account, err := getOrCreateAccount(cmdMsg.Command.UserId, true)
				if err != nil {
					log.WithError(err).Error("Failed fetching coinbase account.")
					sayError(cmdMsg, err.Error(), false)
					return
				}
				say(cmdMsg, fmt.Sprintf("tipjar balance: %s", accountBalanceString(account)), false)
			case "deposit":
				account, err := getOrCreateAccount(cmdMsg.Command.UserId, false)
				if err != nil {
					log.WithError(err).Error("Failed fetching coinbase account.")
					sayError(cmdMsg, err.Error(), false)
					return
				}
				address, err := coinbaseClient.CreateAddress(account.ID)
				if err != nil {
					log.WithError(err).Error("Failed fetching coinbase address.")
					sayError(cmdMsg, err.Error(), false)
					return
				}
				say(cmdMsg, fmt.Sprintf("deposit address: %s", address), false)
			case "withdraw":
				say(cmdMsg, "withdraw is not implemented yet, sorry!", false)
			default:
				help(cmdMsg)
				return
			}

		case <-ctx.Done():
			return
		}
	}
}

func Register(apiKey, apiSecret, bankAccountId string) quadlek.Plugin {
	client, err := cointip.APIKeyClient(apiKey, apiSecret)
	if err != nil {
		return nil
	}
	coinbaseClient = client

	// Warm the cache and fetch the bank account
	account, err := getOrCreateAccount(bankAccountId, true)
	if err != nil {
		return nil
	}
	bankAccount = account

	return quadlek.MakePlugin(
		"cointip",
		[]quadlek.Command{
			quadlek.MakeCommand("cointip", cointipCommand),
		},
		nil,
		[]quadlek.ReactionHook{
			quadlek.MakeReactionHook(cointipReaction),
		},
		nil,
		nil,
	)
}
