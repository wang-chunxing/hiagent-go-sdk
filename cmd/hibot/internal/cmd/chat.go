package cmd

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/volcengine/hiagent-go-sdk/hibot"
)

func newChatCmd(v *viper.Viper) *cobra.Command {
	var (
		input           string
		stream          bool
		clientMessageID string
		agentID         string
		filePaths       []string
	)
	cmd := &cobra.Command{
		Use:   "chat <session-id>",
		Short: "Send a chat message",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			sessionID := args[0]
			if _, err := uuid.Parse(sessionID); err != nil {
				return newUserError("session id must be a valid UUID; create one with `hibot sessions create --agent-id <agent-id>`")
			}
			text, err := resolveChatInput(cmd, input, len(filePaths) > 0)
			if err != nil {
				return err
			}
			client, err := buildClient(v)
			if err != nil {
				return err
			}
			params := hibot.V1SessionChatParams{
				AgentID:         agentID,
				Input:           text,
				ClientMessageID: clientMessageID,
			}
			verbose, _ := cmd.Flags().GetBool(flagVerbose)
			ctx := context.Background()
			out := cmd.OutOrStdout()
			files, err := uploadChatFiles(ctx, client, filePaths)
			if err != nil {
				return err
			}

			if !stream {
				params.Files = files
				msg, err := client.V1.Sessions.Chat(ctx, sessionID, params)
				if err != nil {
					return err
				}
				format := resolveOutputFormat(cmd)
				e := newEmitter(format, out)
				return e.emitObject(msg,
					[]string{"ID", "ROLE", "CONTENT"},
					[][]string{{msg.ID, msg.Role, msg.Content}})
			}
			// Streaming path: write deltas directly, end with [completed].
			params.Files = files
			s := client.V1.Sessions.ChatStreaming(ctx, sessionID, params)
			defer s.Close()
			return runStreamingChat(s, out, verbose)
		},
	}
	cmd.Flags().StringVar(&input, "input", "", "Chat input text (default: read from stdin)")
	cmd.Flags().StringVar(&agentID, "agent-id", "", "Agent ID bound to the session (optional; resolved from session when omitted)")
	cmd.Flags().StringArrayVar(&filePaths, "file", nil, "Attach a local file; repeat for multiple files")
	cmd.Flags().BoolVar(&stream, "stream", false, "Stream deltas to stdout")
	cmd.Flags().StringVar(&clientMessageID, "client-message-id", "", "Idempotency key for the user message")
	return cmd
}

// resolveChatInput resolves --input or, when missing, reads from stdin until EOF.
func resolveChatInput(cmd *cobra.Command, flagValue string, allowEmpty bool) (string, error) {
	if flagValue != "" {
		return readContentArg(flagValue)
	}
	stdin := cmd.InOrStdin()
	// Avoid blocking forever when stdin is a TTY with no input.
	if f, ok := stdin.(*os.File); ok {
		if info, err := f.Stat(); err == nil && (info.Mode()&os.ModeCharDevice) != 0 {
			if allowEmpty {
				return "", nil
			}
			return "", newUserError("--input is required (or pipe data into stdin)")
		}
	}
	data, err := io.ReadAll(bufio.NewReader(stdin))
	if err != nil {
		return "", err
	}
	text := strings.TrimRight(string(data), "\r\n")
	if text == "" {
		if allowEmpty {
			return "", nil
		}
		return "", newUserError("--input is required (or pipe data into stdin)")
	}
	return text, nil
}

func uploadChatFiles(ctx context.Context, client *hibot.Client, paths []string) ([]hibot.V1MessageFile, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	files := make([]hibot.V1MessageFile, 0, len(paths))
	for _, path := range paths {
		f, err := os.Open(path)
		if err != nil {
			return nil, fmt.Errorf("open chat file %s: %w", path, err)
		}
		contentType := mime.TypeByExtension(filepath.Ext(path))
		if contentType == "" {
			contentType = "application/octet-stream"
		}
		blob, uploadErr := client.V1.Uploads.UploadBlob(ctx, hibot.V1UploadBlobParams{
			Filename:    filepath.Base(path),
			ContentType: contentType,
		}, f)
		closeErr := f.Close()
		if uploadErr != nil {
			return nil, fmt.Errorf("upload chat file %s: %w", path, uploadErr)
		}
		if closeErr != nil {
			return nil, fmt.Errorf("close chat file %s: %w", path, closeErr)
		}
		files = append(files, hibot.V1MessageFile{
			Name:        filepath.Base(path),
			ContentType: contentType,
			BlobID:      blob.BlobID,
		})
	}
	return files, nil
}

// runStreamingChat consumes the stream and writes delta text to w. completed
// triggers `\n[completed message_id=...]`; failed becomes an error. Other
// events are silenced unless verbose=true.
func runStreamingChat(s *hibot.V1SessionChatStream, w io.Writer, verbose bool) error {
	completedPrinted := false
	for s.Next() {
		event := s.Current()
		switch event.Type {
		case hibot.V1SessionChatEventDelta:
			if event.Delta.Text != "" {
				_, _ = io.WriteString(w, event.Delta.Text)
			}
		case hibot.V1SessionChatEventCompleted:
			if event.Message == nil {
				if verbose {
					fmt.Fprintf(w, "\n[event:%s]\n", event.Type)
				}
				continue
			}
			if completedPrinted {
				continue
			}
			completedPrinted = true
			id := event.Message.ID
			fmt.Fprintf(w, "\n[completed message_id=%s]\n", id)
		case hibot.V1SessionChatEventFailed:
			msg := event.Error.Message
			if msg == "" {
				msg = event.Error.Code
			}
			return fmt.Errorf("chat failed: %s", msg)
		default:
			if verbose && event.Type != "" {
				fmt.Fprintf(w, "\n[event:%s]\n", event.Type)
			}
		}
	}
	return s.Err()
}
