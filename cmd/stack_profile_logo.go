package cmd

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

// The uploaded-logo commands: upload validated image bytes for an owned
// profile, download the stored logo, and remove it. A logo set by URL
// instead of upload is 'update --logo-url'.

var stackProfilesLogoCmd = &cobra.Command{
	Use:   "logo",
	Short: "Manage a profile's uploaded catalogue logo",
	Long: `Manage the uploaded logo a profile shows in the catalogue. Uploading
stores the image bytes with Ankra and clears any logo URL; to reference an
image by URL instead, use 'ankra stack-profiles update --logo-url'.`,
}

// stackProfileLogoExtensions maps served content types to a download file
// extension; the upload direction sniffs the content instead.
var stackProfileLogoExtensions = map[string]string{
	"image/png":  ".png",
	"image/jpeg": ".jpg",
	"image/webp": ".webp",
}

var stackProfilesLogoGetCmd = &cobra.Command{
	Use:   "get [profile-id|profile-name]",
	Short: "Download a profile's uploaded logo",
	Long: `Download the uploaded logo's image bytes. Without --output the file is
named after the profile reference with the served image type's extension.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		profileID, resolveError := resolveStackProfileID(apiClient, args[0])
		if resolveError != nil {
			return resolveError
		}
		logo, getError := apiClient.GetStackProfileLogo(cmd.Context(), profileID)
		if getError != nil {
			return fmt.Errorf("downloading stack profile logo: %w", getError)
		}
		outputPath, _ := cmd.Flags().GetString("output")
		if outputPath == "" {
			extension := stackProfileLogoExtensions[logo.ContentType]
			if extension == "" {
				extension = ".img"
			}
			outputPath = strings.ReplaceAll(args[0], string(filepath.Separator), "-") + extension
		}
		if writeError := os.WriteFile(outputPath, logo.Content, 0o600); writeError != nil {
			return fmt.Errorf("writing %s: %w", outputPath, writeError)
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Saved %s logo (%s, %d bytes) to %s\n",
			args[0], logo.ContentType, len(logo.Content), outputPath)
		return nil
	},
}

var stackProfilesLogoSetCmd = &cobra.Command{
	Use:   "set [profile-id|profile-name] <image-file>",
	Short: "Upload a profile's catalogue logo",
	Long: `Upload a logo image for a profile you own. PNG, JPEG, and WebP are
accepted, at most 512 KiB; large images are resized automatically and
square images look best. The upload clears any stored logo URL.`,
	Example: "  ankra stack-profiles logo set postgres-ha ./logo.png",
	Args:    cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		if _, formatError := structuredFormatFromFlags(cmd); formatError != nil {
			return formatError
		}
		profileID, resolveError := resolveStackProfileID(apiClient, args[0])
		if resolveError != nil {
			return resolveError
		}
		content, readError := os.ReadFile(args[1])
		if readError != nil {
			return fmt.Errorf("reading %s: %w", args[1], readError)
		}
		contentType := http.DetectContentType(content)
		if !strings.HasPrefix(contentType, "image/") {
			return withExitCode(exitUsage, errors.New(
				"the file does not look like an image; PNG, JPEG, and WebP are accepted"))
		}
		payload, putError := apiClient.PutStackProfileLogo(cmd.Context(), profileID, content, contentType)
		if putError != nil {
			return fmt.Errorf("uploading stack profile logo: %w", putError)
		}
		return renderApplicationPayload(cmd, payload)
	},
}

var stackProfilesLogoClearCmd = &cobra.Command{
	Use:   "clear [profile-id|profile-name]",
	Short: "Remove a profile's uploaded logo",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if _, formatError := structuredFormatFromFlags(cmd); formatError != nil {
			return formatError
		}
		profileID, resolveError := resolveStackProfileID(apiClient, args[0])
		if resolveError != nil {
			return resolveError
		}
		payload, deleteError := apiClient.DeleteStackProfileLogo(cmd.Context(), profileID)
		if deleteError != nil {
			return fmt.Errorf("removing stack profile logo: %w", deleteError)
		}
		return renderApplicationPayload(cmd, payload)
	},
}

func init() {
	stackProfilesLogoGetCmd.Flags().String("output", "", "Write the image to this file (defaults to <profile><ext>)")

	registerStructuredOutputFlags(stackProfilesLogoSetCmd)
	registerStructuredOutputFlags(stackProfilesLogoClearCmd)

	stackProfilesLogoCmd.AddCommand(
		stackProfilesLogoGetCmd,
		stackProfilesLogoSetCmd,
		stackProfilesLogoClearCmd,
	)
	stackProfilesCmd.AddCommand(stackProfilesLogoCmd)
}
