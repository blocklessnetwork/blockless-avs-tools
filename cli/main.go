package main

import (
	"os"

	sdkecdsa "github.com/Layr-Labs/eigensdk-go/crypto/ecdsa"
	"github.com/rs/zerolog/log"
	"github.com/urfave/cli/v2"
	avs "github.com/zees-dev/blockless-avs"
	"github.com/zees-dev/blockless-avs/core/config"
	"github.com/zees-dev/blockless-avs/core/logging"
)

const AppName = "Blockless AVS Tools"

func main() {
	app := cli.NewApp()

	app.Name = AppName
	app.Usage = "Tools for interacting with Blockless AVS contracts"

	// globally required flags
	// app.Flags = []cli.Flag{
	// 	config.DevModeFlag,
	// 	config.ConfigFileFlag,
	// 	config.HeadlessFlag,
	// }

	// init app state, store in context
	app.Before = func(c *cli.Context) error {
		logger := logging.NewZeroLogger(logging.Development)

		// setup operator from config file - provided as flag
		// devMode := c.Bool(config.DevModeFlag.Name)
		// configPath := c.String(config.ConfigFileFlag.Name)
		// headless := c.Bool(config.HeadlessFlag.Name)

		// nodeConfig := types.NodeConfig{}
		// if err := sdkutils.ReadYamlConfig(configPath, &nodeConfig); err != nil {
		// 	return err
		// }
		// operator, err := operator.NewOperatorFromConfig(logger, nodeConfig)
		// if err != nil {
		// 	return err
		// }

		// if !headless {
		// 	return errors.New("only headless mode is supported")
		// }

		c.App.Metadata[avs.AppConfigKey] = &avs.AppConfig{
			AppName: AppName,
			Logger:  logger,
			// NodeConfig: &nodeConfig,
			// Operator:   operator,
			// DevMode:    devMode,
			// Headless:   headless,
		}
		return nil
	}

	app.Commands = []*cli.Command{
		// {
		// 	Name:   "run-avs",
		// 	Usage:  "Starts the server",
		// 	Action: RunAVS,
		// 	Before: func(c *cli.Context) error {
		// 		// get app config
		// 		app := c.App.Metadata[avs.AppConfigKey].(*avs.AppConfig)

		// 		b7sConfig := node.ParseFlags(c)
		// 		app.BlocklessConfig = &b7sConfig
		// 		return nil
		// 	},
		// 	Flags: []cli.Flag{
		// 		node.Role,
		// 		node.PeerDatabasePath,
		// 		node.FunctionDatabasePath,
		// 		node.Workspace,
		// 		node.Concurrency,
		// 		node.LoadAttributes,
		// 		node.PrivateKey,
		// 		node.HostAddress,
		// 		node.HostPort,
		// 		node.BootNodes,
		// 		node.DialBackAddress,
		// 		node.DialBackPort,
		// 		node.Websocket,
		// 		node.WebsocketPort,
		// 		node.DialBackWebsocketPort,
		// 		node.CPUPercentage,
		// 		node.MemoryMaxKB,
		// 	},
		// },
		// {
		// 	Name:    "register-operator-with-eigenlayer",
		// 	Aliases: []string{"rel"},
		// 	Usage:   "registers operator with eigenlayer (this should be called via eigenlayer cli, not plugin, but keeping here for convenience for now)",
		// 	Action: func(ctx *cli.Context) error {
		// 		operator := ctx.App.Metadata[avs.AppConfigKey].(*avs.AppConfig).Operator
		// 		return operator.RegisterOperatorWithEigenlayer()
		// 	},
		// 	Flags: []cli.Flag{config.ConfigFileFlag},
		// },
		// {
		// 	Name:    "deposit-into-strategy",
		// 	Aliases: []string{"dis"},
		// 	Usage:   "deposit tokens into a strategy",
		// 	Action: func(ctx *cli.Context) error {
		// 		app := ctx.App.Metadata[avs.AppConfigKey].(*avs.AppConfig)

		// 		strategyAddrStr := app.NodeConfig.TokenStrategyAddr
		// 		strategyAddr := common.HexToAddress(strategyAddrStr)
		// 		amountStr := ctx.String("amount")
		// 		amount, ok := new(big.Int).SetString(amountStr, 10)
		// 		if !ok {
		// 			app.Logger.Error("Error converting amount to big.Int")
		// 			return errors.New("Error converting amount to big.Int")
		// 		}
		// 		return app.Operator.DepositIntoStrategy(strategyAddr, amount)
		// 	},
		// 	Flags: []cli.Flag{
		// 		config.ConfigFileFlag,
		// 		// &cli.StringFlag{
		// 		// 	Name:     "strategy-addr",
		// 		// 	Usage:    "Address of Strategy contract to deposit into",
		// 		// 	Required: true,
		// 		// },
		// 		&cli.StringFlag{
		// 			Name:     "amount",
		// 			Usage:    "amount of tokens to deposit into strategy",
		// 			Required: true,
		// 		},
		// 	},
		// },
		{
			Name:    "register-operator-with-avs",
			Aliases: []string{"rowa"},
			Usage:   "registers bls keys with pubkey-compendium, opts into slashing by avs service-manager, and registers operators with avs registry",
			Action: func(ctx *cli.Context) error {
				app := ctx.App.Metadata[avs.AppConfigKey].(*avs.AppConfig)
				ecdsaKeyPassword, ok := os.LookupEnv("OPERATOR_ECDSA_KEY_PASSWORD")
				if !ok {
					app.Logger.Info("OPERATOR_ECDSA_KEY_PASSWORD env var not set. using empty string")
				}
				operatorEcdsaPrivKey, err := sdkecdsa.ReadKey(
					app.NodeConfig.EcdsaPrivateKeyStorePath,
					ecdsaKeyPassword,
				)
				if err != nil {
					return err
				}

				return app.Operator.RegisterOperatorWithAvs(operatorEcdsaPrivKey)
			},
			Flags: []cli.Flag{config.ConfigFileFlag},
		},
		{
			Name:    "deregister-operator-with-avs",
			Aliases: []string{"dowa"},
			Action: func(ctx *cli.Context) error {
				app := ctx.App.Metadata[avs.AppConfigKey].(*avs.AppConfig)
				app.Logger.Fatal("Command not implemented.")
				return nil
			},
			Flags: []cli.Flag{config.ConfigFileFlag},
		},
		{
			Name:    "print-operator-status",
			Aliases: []string{"pos"},
			Usage:   "prints operator status as viewed from blockless-avs contracts",
			Action: func(ctx *cli.Context) error {
				operator := ctx.App.Metadata[avs.AppConfigKey].(*avs.AppConfig).Operator
				return operator.PrintOperatorStatus()
			},
			Flags: []cli.Flag{config.ConfigFileFlag},
		},
	}

	if err := app.Run(os.Args); err != nil {
		log.Fatal().Err(err).Msg("Failed to run app")
	}
}
