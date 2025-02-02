package restservice

import (
	"context"
	"fmt"
	"net/http"

	"github.com/go-openapi/runtime/middleware"
	"github.com/go-openapi/strfmt"
	log "github.com/sirupsen/logrus"

	"isc.org/stork/server/configreview"
	dbmodel "isc.org/stork/server/database/model"
	"isc.org/stork/server/gen/models"
	"isc.org/stork/server/gen/restapi/operations/services"
	storkutil "isc.org/stork/util"
)

// Get daemon config. Only Kea daemon supported.
func (r *RestAPI) GetDaemonConfig(ctx context.Context, params services.GetDaemonConfigParams) middleware.Responder {
	dbDaemon, err := dbmodel.GetDaemonByID(r.DB, params.ID)
	if err != nil {
		log.Error(err)
		msg := fmt.Sprintf("cannot get daemon with id %d from db", params.ID)
		rsp := services.NewGetDaemonConfigDefault(http.StatusInternalServerError).WithPayload(&models.APIError{
			Message: &msg,
		})
		return rsp
	}
	if dbDaemon == nil {
		msg := fmt.Sprintf("cannot find daemon with id %d", params.ID)
		rsp := services.NewGetDaemonConfigDefault(http.StatusBadRequest).WithPayload(&models.APIError{
			Message: &msg,
		})
		return rsp
	}

	if dbDaemon.KeaDaemon == nil {
		msg := fmt.Sprintf("daemon with id %d isn't Kea daemon", params.ID)
		rsp := services.NewGetDaemonConfigDefault(http.StatusBadRequest).WithPayload(&models.APIError{
			Message: &msg,
		})
		return rsp
	}
	if dbDaemon.KeaDaemon.Config == nil {
		msg := fmt.Sprintf("config not assigned for daemon with id %d", params.ID)
		rsp := services.NewGetDaemonConfigDefault(http.StatusNotFound).WithPayload(&models.APIError{
			Message: &msg,
		})
		return rsp
	}

	_, dbUser := r.SessionManager.Logged(ctx)
	if !dbUser.InGroup(&dbmodel.SystemGroup{ID: dbmodel.SuperAdminGroupID}) {
		storkutil.HideSensitiveData((*map[string]interface{})(dbDaemon.KeaDaemon.Config))
	}

	rsp := services.NewGetDaemonConfigOK().WithPayload(dbDaemon.KeaDaemon.Config)
	return rsp
}

// Get configuration review reports for a specified daemon. Only Kea
// daemons are currently supported. The daemon id value is mandatory.
// The start and limit values are optional. They are used to retrieve
// paged configuration review reports for a daemon. If they are not
// specified, all configuration reports are returned. When the
// configuration review is in progress for the specified daemon it
// returns HTTP Accepted status code. When the review hasn't been
// yet performed for the daemon, it returns HTTP No Content status
// code. If the review is available it returns HTTP OK status code.
func (r *RestAPI) GetDaemonConfigReports(ctx context.Context, params services.GetDaemonConfigReportsParams) middleware.Responder {
	// If the review is in progress return HTTP Accepted status
	// code to indicate that the caller can try again soon to
	// get the new reports.
	if r.ReviewDispatcher.ReviewInProgress(params.ID) {
		rsp := services.NewGetDaemonConfigReportsAccepted()
		return rsp
	}

	// Get the basic information about the last review.
	review, err := dbmodel.GetConfigReviewByDaemonID(r.DB, params.ID)
	if err != nil {
		log.Error(err)
		msg := fmt.Sprintf("cannot get configuration review for daemon with id %d from db", params.ID)
		rsp := services.NewGetDaemonConfigReportsDefault(http.StatusInternalServerError).WithPayload(&models.APIError{
			Message: &msg,
		})
		return rsp
	}
	if review == nil {
		// If the information is not present it means that the daemon
		// configuration has never been reviewed. HTTP No Content status
		// indicates it to the client.
		rsp := services.NewGetDaemonConfigReportsNoContent()
		return rsp
	}

	start := int64(0)
	if params.Start != nil {
		start = *params.Start
	}

	limit := int64(0)
	if params.Limit != nil {
		limit = *params.Limit
	}

	dbReports, total, err := dbmodel.GetConfigReportsByDaemonID(r.DB, start, limit, params.ID)
	if err != nil {
		log.Error(err)
		msg := fmt.Sprintf("cannot get configuration review reports for daemon with id %d from db", params.ID)
		rsp := services.NewGetDaemonConfigReportsDefault(http.StatusInternalServerError).WithPayload(&models.APIError{
			Message: &msg,
		})
		return rsp
	}

	configReports := &models.ConfigReports{
		Review: &models.ConfigReview{
			ID:        review.ID,
			DaemonID:  review.DaemonID,
			CreatedAt: strfmt.DateTime(review.CreatedAt),
		},
		Total: total,
	}

	for _, dbReport := range dbReports {
		report := &models.ConfigReport{
			ID:        dbReport.ID,
			CreatedAt: strfmt.DateTime(dbReport.CreatedAt),
			Checker:   dbReport.CheckerName,
			Content:   dbReport.Content,
		}
		configReports.Items = append(configReports.Items, report)
	}

	rsp := services.NewGetDaemonConfigReportsOK().WithPayload(configReports)
	return rsp
}

// Begins daemon configuration review on demand.
func (r *RestAPI) PutDaemonConfigReview(ctx context.Context, params services.PutDaemonConfigReviewParams) middleware.Responder {
	// Try to get the daemon information from the database.
	daemon, err := dbmodel.GetDaemonByID(r.DB, params.ID)
	if err != nil {
		log.Error(err)
		msg := fmt.Sprintf("cannot get daemon with id %d from db", params.ID)
		rsp := services.NewPutDaemonConfigReviewDefault(http.StatusInternalServerError).WithPayload(&models.APIError{
			Message: &msg,
		})
		return rsp
	}
	// If the daemon doesn't exist there is nothing to do. Return the
	// HTTP Bad Request status.
	if daemon == nil {
		msg := fmt.Sprintf("cannot find daemon with id %d", params.ID)
		rsp := services.NewPutDaemonConfigReviewDefault(http.StatusBadRequest).WithPayload(&models.APIError{
			Message: &msg,
		})
		return rsp
	}
	// Config review is currently only supported for Kea.
	if daemon.KeaDaemon == nil {
		msg := fmt.Sprintf("daemon with id %d is not a Kea daemon", params.ID)
		rsp := services.NewPutDaemonConfigReviewDefault(http.StatusBadRequest).WithPayload(&models.APIError{
			Message: &msg,
		})
		return rsp
	}
	// Config must be present to perform the review.
	if daemon.KeaDaemon.Config == nil {
		msg := fmt.Sprintf("configuration not found for daemon with id %d", params.ID)
		rsp := services.NewPutDaemonConfigReviewDefault(http.StatusBadRequest).WithPayload(&models.APIError{
			Message: &msg,
		})
		return rsp
	}

	// Begin the review but do not wait for the result.
	_ = r.ReviewDispatcher.BeginReview(daemon, configreview.ManualRun, nil)

	// Inform the caller that the review request has been "accepted".
	rsp := services.NewPutDaemonConfigReviewAccepted()
	return rsp
}
