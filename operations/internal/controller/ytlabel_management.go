package controller

import "github.com/streamfusion-ai/operations/internal/serviceinstall"

func ytlabelManagementDefinition() *serviceinstall.ManagementDefinition {
	return annotationManagementDefinition("ytlabel", "NATIVE")
}
