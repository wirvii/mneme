package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/wirvii/mneme/internal/config"
	"github.com/wirvii/mneme/internal/service"
	"github.com/wirvii/mneme/internal/speech"
)

func newSpeechCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "speech", Short: "Control local spoken responses"}
	cmd.AddCommand(
		speechToggleCmd("on", true), speechToggleCmd("off", false),
		&cobra.Command{Use: "stop", Short: "Stop current speech", RunE: func(cmd *cobra.Command, _ []string) error { return speechService().Stop(cmd.Context()) }},
		newSpeechStatusCmd(), newSpeechModeCmd(), newSpeechVoiceCmd(), newSpeechTestCmd(), newSpeechVoicesCmd(), newSpeechSetupCmd(), newSpeechSuperviseCmd(),
	)
	return cmd
}

func speechService() *service.SpeechService {
	return service.NewSpeechService(config.DefaultPath(), flagDataDir)
}

func speechToggleCmd(name string, enabled bool) *cobra.Command {
	return &cobra.Command{Use: name, Short: name + " spoken responses", RunE: func(cmd *cobra.Command, _ []string) error {
		if err := speechService().SetEnabled(cmd.Context(), enabled); err != nil {
			return err
		}
		if enabled {
			fmt.Fprintln(cmd.OutOrStdout(), "Speech enabled (brief mode by default).")
		} else {
			fmt.Fprintln(cmd.OutOrStdout(), "Speech disabled.")
		}
		return nil
	}}
}

func newSpeechStatusCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{Use: "status", Short: "Show speech state", RunE: func(cmd *cobra.Command, _ []string) error {
		ctx, cancel := context.WithTimeout(cmd.Context(), 300*time.Millisecond)
		defer cancel()
		status, err := speechService().Status(ctx)
		if err != nil {
			return err
		}
		if asJSON {
			return json.NewEncoder(cmd.OutOrStdout()).Encode(status)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "enabled: %t\nmode: %s\nconfigured engine: %s\npreferred engine: %s\neffective engine: %s\npreferred voice: %s\neffective voice: %s\npreference source: %s\ndegraded: %t\nlanguage: %s\nfallback language: %s\nsetup ready: %t\nspeaking: %t\nemitted: %d\nskipped: %d\nmissed turns: %d\nlast error: %s\n", status.Enabled, status.Mode, status.ConfiguredEngine, status.PreferredEngine, status.Engine, status.PreferredVoice, status.EffectiveVoice, status.PreferenceSource, status.Degraded, status.Language, status.FallbackLanguage, status.SetupReady, status.Speaking, status.Emitted, status.Skipped, status.MissedTurns, status.LastError)
		return nil
	}}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output JSON")
	return cmd
}

func newSpeechVoiceCmd() *cobra.Command {
	voiceCmd := &cobra.Command{Use: "voice", Short: "Manage per-language speech voices"}
	var language, engine, voice, fallbackEngine, fallbackVoice string
	setCmd := &cobra.Command{Use: "set", Short: "Set the preferred engine and voice for a language", RunE: func(cmd *cobra.Command, _ []string) error {
		if language == "" || engine == "" || voice == "" {
			return errors.New("speech voice set requires --language, --engine, and --voice")
		}
		if err := speechService().SetLanguagePreference(language, engine, voice, fallbackEngine, fallbackVoice); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Speech preference for %s: %s/%s.\n", language, engine, voice)
		return nil
	}}
	setCmd.Flags().StringVar(&language, "language", "", "BCP-47 language, such as es or es-MX")
	setCmd.Flags().StringVar(&engine, "engine", "", "Preferred engine")
	setCmd.Flags().StringVar(&voice, "voice", "", "Preferred voice")
	setCmd.Flags().StringVar(&fallbackEngine, "fallback-engine", "", "Fallback engine")
	setCmd.Flags().StringVar(&fallbackVoice, "fallback-voice", "", "Fallback voice")
	voiceCmd.AddCommand(setCmd)
	return voiceCmd
}

func newSpeechModeCmd() *cobra.Command {
	return &cobra.Command{Use: "mode <brief|full>", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		if err := speechService().SetMode(speech.Mode(args[0])); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Speech mode: %s.\n", args[0])
		return nil
	}}
}

func newSpeechTestCmd() *cobra.Command {
	var mode, voice string
	cmd := &cobra.Command{Use: "test [text]", Args: cobra.MaximumNArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		text := "Mneme puede hablar localmente."
		if len(args) > 0 {
			text = args[0]
		}
		return speechService().EmitWithOverrides(cmd.Context(), speech.DispositionEmit, speech.Mode(mode), text, "es", voice)
	}}
	cmd.Flags().StringVar(&mode, "mode", "brief", "Speech mode")
	cmd.Flags().StringVar(&voice, "voice", "", "Temporary voice override")
	return cmd
}

func newSpeechVoicesCmd() *cobra.Command {
	var asJSON bool
	var engine string
	cmd := &cobra.Command{Use: "voices", RunE: func(cmd *cobra.Command, _ []string) error {
		voices, err := speechService().ListVoicesFor(cmd.Context(), engine)
		if err != nil {
			return err
		}
		if asJSON {
			return json.NewEncoder(cmd.OutOrStdout()).Encode(voices)
		}
		for _, voice := range voices {
			fmt.Fprintln(cmd.OutOrStdout(), voice)
		}
		return nil
	}}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output JSON")
	cmd.Flags().StringVar(&engine, "engine", "", "Filter by engine")
	return cmd
}

func newSpeechSetupCmd() *cobra.Command {
	var model string
	var checksum string
	cmd := &cobra.Command{Use: "setup", Short: "Configure a local Piper model on Linux", RunE: func(cmd *cobra.Command, _ []string) error {
		if model == "" || checksum == "" {
			return errors.New("speech setup requires --model PATH and --sha256 DIGEST; mneme never downloads models")
		}
		svc := speechService()
		if err := svc.SetupLocalModel(model, checksum); err != nil {
			return err
		}
		status, err := svc.Status(cmd.Context())
		if err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), "Local Piper model configured; speech remains", map[bool]string{true: "enabled.", false: "disabled."}[status.Enabled])
		return nil
	}}
	cmd.Flags().StringVar(&model, "model", "", "Existing local Piper .onnx model")
	cmd.Flags().StringVar(&checksum, "sha256", "", "Expected SHA-256 digest of the local model")
	return cmd
}

func newSpeechSuperviseCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "supervise", Hidden: true, RunE: func(cmd *cobra.Command, _ []string) error {
		dataDir := flagDataDir
		if strings.TrimSpace(dataDir) == "" {
			dataDir = config.Default().Storage.DataDir
		}
		return speech.Supervise(cmd.Context(), dataDir)
	}}
	return cmd
}
