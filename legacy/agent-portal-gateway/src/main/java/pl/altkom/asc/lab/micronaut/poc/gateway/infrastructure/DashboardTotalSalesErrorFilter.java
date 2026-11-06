package pl.altkom.asc.lab.micronaut.poc.gateway.infrastructure;

import io.micronaut.core.convert.exceptions.ConversionErrorException;
import io.micronaut.http.HttpRequest;
import io.micronaut.http.HttpResponse;
import io.micronaut.http.HttpStatus;
import io.micronaut.http.MediaType;
import io.micronaut.http.MutableHttpResponse;
import io.micronaut.http.annotation.Filter;
import io.micronaut.http.codec.CodecException;
import io.micronaut.http.exceptions.HttpStatusException;
import io.micronaut.http.filter.HttpServerFilter;
import io.micronaut.http.filter.ServerFilterChain;
import io.micronaut.security.authentication.AuthorizationException;
import io.micronaut.web.router.exceptions.UnsatisfiedRouteException;
import io.reactivex.Flowable;
import org.reactivestreams.Publisher;

import java.util.Collections;
import java.util.LinkedHashMap;
import java.util.Map;

@Filter("/api/dashboard/*sales")
public class DashboardTotalSalesErrorFilter implements HttpServerFilter {

    @Override
    public Publisher<MutableHttpResponse<?>> doFilter(HttpRequest<?> request, ServerFilterChain chain) {
        return Flowable.fromPublisher(chain.proceed(request))
                .map(this::sanitizeResponse)
                .onErrorReturn(this::sanitizeFailure);
    }

    private MutableHttpResponse<?> sanitizeResponse(MutableHttpResponse<?> response) {
        HttpStatus status = response.getStatus();
        if (status == HttpStatus.UNAUTHORIZED || status == HttpStatus.FORBIDDEN
                || status.getCode() < 400) {
            return response;
        }
        return response.contentType(MediaType.APPLICATION_JSON_TYPE).body(safeBody(status));
    }

    private MutableHttpResponse<?> safeResponse(HttpStatus status) {
        MutableHttpResponse<?> response = HttpResponse.status(status)
                .contentType(MediaType.APPLICATION_JSON_TYPE)
                .body(safeBody(status));
        if (status == HttpStatus.METHOD_NOT_ALLOWED) {
            response.header("Allow", "POST");
        }
        return response;
    }

    private MutableHttpResponse<?> sanitizeFailure(Throwable error) {
        if (error instanceof AuthorizationException) {
            AuthorizationException authorization = (AuthorizationException) error;
            return HttpResponse.status(authorization.isForbidden()
                    ? HttpStatus.FORBIDDEN : HttpStatus.UNAUTHORIZED);
        }
        if (error instanceof ConversionErrorException || error instanceof CodecException
                || error instanceof UnsatisfiedRouteException) {
            return safeResponse(HttpStatus.BAD_REQUEST);
        }
        if (error instanceof HttpStatusException) {
            HttpStatus status = ((HttpStatusException) error).getStatus();
            if (status == HttpStatus.BAD_REQUEST || status == HttpStatus.METHOD_NOT_ALLOWED
                    || status == HttpStatus.UNSUPPORTED_MEDIA_TYPE) {
                return safeResponse(status);
            }
        }
        return safeResponse(HttpStatus.INTERNAL_SERVER_ERROR);
    }

    private Map<String, Object> safeBody(HttpStatus status) {
        int code = 13;
        String message = "internal error";
        if (status == HttpStatus.BAD_REQUEST) {
            code = 3;
            message = "invalid request";
        } else if (status == HttpStatus.METHOD_NOT_ALLOWED) {
            code = 12;
            message = "method not allowed";
        } else if (status == HttpStatus.UNSUPPORTED_MEDIA_TYPE) {
            code = 3;
            message = "unsupported media type";
        }
        Map<String, Object> body = new LinkedHashMap<>();
        body.put("code", code);
        body.put("message", message);
        body.put("details", Collections.emptyList());
        return body;
    }
}
