package pkg

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	admissionv1 "k8s.io/api/admission/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/klog"
	"net/http"
	"strings"
)

var (
	runtimeScheme = runtime.NewScheme()
	codeFactory   = serializer.NewCodecFactory(runtimeScheme)
	deserializer  = codeFactory.UniversalDeserializer()
)

const (
	AnnotationMutateKey = "zhf/mutate" // zhf/mutate=no/off/false/n
	AnnotationStatusKey = "zhf/status" // zhf/status=mutated
)

type patchOperation struct {
	Op    string      `json:"op"`
	Path  string      `json:"path"`
	Value interface{} `json:"value,omitempty"`
}

type WhSvrParam struct {
	Port     int
	CertFile string
	KeyFile  string
}

type WebhookServer struct {
	Server              *http.Server
	WhiteListRegistries []string
}

func (s WebhookServer) Handler(writer http.ResponseWriter, request *http.Request) {
	var body []byte
	if request.Body != nil {
		if data, err := ioutil.ReadAll(request.Body); err == nil {
			body = data
		}
	}

	if len(body) == 0 {
		klog.Error("empty data body")
		klog.Error(writer, "empty data body", http.StatusBadRequest)
		return
	}

	// 校验 content-type

	contentType := request.Header.Get("Content-Type")
	if contentType != "application/json" {
		klog.Errorf("content-type is %s ,but expect application/json", contentType)
		http.Error(writer, "content-type invalid,expect application/json", http.StatusBadRequest)
		return
	}

	// 序列化请求的数据
	var admissionResponse *admissionv1.AdmissionResponse
	requestedAdmissionReview := admissionv1.AdmissionReview{}

	if _, _, err := deserializer.Decode(body, nil, &requestedAdmissionReview); err != nil {
		klog.Errorf("cant decode body: %v", err)
		admissionResponse = &admissionv1.AdmissionResponse{
			Result: &metav1.Status{
				Code:    http.StatusInternalServerError,
				Message: err.Error(),
			},
		}
	} else {

		// 开始处理方法进行调用
		if request.URL.Path == "/mutate" {
			admissionResponse = s.mutate(&requestedAdmissionReview)

		} else if request.URL.Path == "/validate" {
			admissionResponse = s.validate(&requestedAdmissionReview)

		}
	}
	responseAdmissionReview := admissionv1.AdmissionReview{}
	responseAdmissionReview.APIVersion = requestedAdmissionReview.APIVersion

	responseAdmissionReview.Kind = requestedAdmissionReview.Kind

	if admissionResponse != nil {
		responseAdmissionReview.Response = admissionResponse
		if requestedAdmissionReview.Request != nil { // 返回相同的 UID
			responseAdmissionReview.Response.UID = requestedAdmissionReview.Request.UID
		}

	}

	klog.Info(fmt.Sprintf("sending response: %v", responseAdmissionReview.Response))
	// send response
	respBytes, err := json.Marshal(responseAdmissionReview)
	if err != nil {
		klog.Errorf("Can't encode response: %v", err)
		http.Error(writer, fmt.Sprintf("Can't encode response: %v", err), http.StatusBadRequest)
		return
	}
	klog.Info("Ready to write response...")

	if _, err := writer.Write(respBytes); err != nil {
		klog.Errorf("Can't write response: %v", err)
		http.Error(writer, fmt.Sprintf("Can't write reponse: %v", err), http.StatusBadRequest)
	}


}

//APIServer 实际上使用的是一个 AdmissionReview 类型的对象来向我们自定义的 Webhook 发送请求和接收响应。

func (s WebhookServer) mutate(a *admissionv1.AdmissionReview) *admissionv1.AdmissionResponse {

	req := a.Request
	var objectMeta *metav1.ObjectMeta

	switch req.Kind.Kind {
	case "Deployment":
		var deployment appsv1.Deployment
		if err := json.Unmarshal(req.Object.Raw, &deployment); err != nil {
			klog.Errorf("cant not unmarshal raw object %v", err)
			return &admissionv1.AdmissionResponse{
				Result: &metav1.Status{
					Code:    http.StatusBadRequest,
					Message: err.Error(),
				},
			}

		}
		objectMeta = &deployment.ObjectMeta
	case "Service":
		var service corev1.Service
		if err := json.Unmarshal(req.Object.Raw, &service); err != nil {
			klog.Errorf("cant unmarshal raw object :%v ", err)
			return &admissionv1.AdmissionResponse{
				Result: &metav1.Status{
					Code:    http.StatusBadRequest,
					Message: err.Error(),
				},
			}
		}
		objectMeta = &service.ObjectMeta

	default:
		return &admissionv1.AdmissionResponse{
			Result: &metav1.Status{
				Code:    http.StatusBadRequest,
				Message: fmt.Sprintf("cant handler the kind %s object ", req.Kind.Kind),
			},
		}

	}
	// 是否需要 mutate
	if !mutationRequired(objectMeta) {
		return &admissionv1.AdmissionResponse{
			Allowed: true,
		}
	}

	annotations := map[string]string{
		AnnotationStatusKey: "mutated",
	}

	var patch []patchOperation

	patch = append(patch, mutateAnnotations(objectMeta.GetAnnotations(), annotations)...)

	patchBytes, err := json.Marshal(patch)
	if err != nil {
		klog.Errorf("patch marshal error: %v", err)
		return &admissionv1.AdmissionResponse{
			Result: &metav1.Status{
				Code:    http.StatusBadRequest,
				Message: err.Error(),
			},
		}
	}

	return &admissionv1.AdmissionResponse{
		Allowed: true,
		Patch:   patchBytes,
		PatchType: func() *admissionv1.PatchType {
			pt := admissionv1.PatchTypeJSONPatch
			return &pt
		}(),
	}

}

func mutateAnnotations(target map[string]string, added map[string]string) (patch []patchOperation) {
	for key, value := range added {
		if target == nil || target[key] == "" {
			target = map[string]string{}
			patch = append(patch, patchOperation{
				Op:   "add",
				Path: "/metadata/annotations",
				Value: map[string]string{
					key: value,
				},
			})
		} else {
			patch = append(patch, patchOperation{
				Op:    "replace",
				Path:  "/metadata/annotations/" + key,
				Value: value,
			})
		}

	}
	return
}

func mutationRequired(meta *metav1.ObjectMeta) bool {

	annotations := meta.GetAnnotations()
	if annotations == nil {
		annotations = map[string]string{}
	}
	var required bool

	switch strings.ToLower(annotations[AnnotationMutateKey]) {
	case "n", "no", "false", "off":
		required = false

	default:
		required = true

	}

	status := annotations[AnnotationMutateKey]
	if strings.ToLower(status) == "mutated" {
		required = false
	}

	klog.Infof("mutation policy for %s/%s ,required %v", meta.Name, meta.Namespace, required)

	return required

}

func (s WebhookServer) validate(a *admissionv1.AdmissionReview) *admissionv1.AdmissionResponse {
	req := a.Request
	var (
		allowed = true
		code    = http.StatusOK
		message = ""
	)

	klog.Info("admission review for  kind %s namespace %s uid $s", req.Kind.Kind, req.Namespace, req.UID)

	var pod corev1.Pod
	if err := json.Unmarshal(req.Object.Raw, &pod); err != nil {
		klog.Errorf("cant unmarshal object raw %v", err)
		allowed = false
		code = http.StatusBadRequest
		return &admissionv1.AdmissionResponse{
			Allowed: allowed,
			Result: &metav1.Status{
				Code:    int32(code),
				Message: err.Error(),
			},
		}

	}

	for _, container := range pod.Spec.Containers {
		var whilelisted = false

		for _, reg := range s.WhiteListRegistries {
			if strings.HasPrefix(container.Image, reg) {
				whilelisted = true
			}

		}

		if !whilelisted {
			allowed = false
			code = http.StatusForbidden
			message = fmt.Sprintf("%s image comes from an untrusted registry! Only images from %v are allowed.", container.Image, s.WhiteListRegistries)
			break
		}

	}

	return &admissionv1.AdmissionResponse{
		Allowed: allowed,
		Result: &metav1.Status{
			Code:    int32(code),
			Message: message,
		},
	}
}
