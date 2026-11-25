// AmfTool is the CLI's bridge to AMF on the JVM. The CLI runs it with the
// pinned AMF classpath as a single source file (java AmfTool.java ...), so it
// needs no build step. Results go to stdout as one JSON object.
//
//   parse URL OUT.jsonld       parse and validate an API spec; transform it
//                              with the editing pipeline and write its JSON-LD
//   dialect DIALECT INSTANCE   parse a dialect instance (a ruleset) and
//                              validate it against the dialect
//   declarations URL           list the types an API spec declares (after the
//                              editing pipeline), with their properties
import amf.aml.client.platform.AMLConfiguration;
import amf.apicontract.client.platform.AMFConfiguration;
import amf.apicontract.client.platform.APIConfiguration;
import amf.core.client.common.transform.PipelineId;
import amf.core.client.platform.AMFParseResult;
import amf.core.client.platform.config.RenderOptions;
import amf.core.client.platform.model.document.BaseUnit;
import amf.core.client.platform.model.document.DeclaresModel;
import amf.core.client.platform.model.domain.DomainElement;
import amf.core.client.platform.model.domain.NamedDomainElement;
import amf.core.client.platform.model.domain.PropertyShape;
import amf.shapes.client.platform.model.domain.NodeShape;
import amf.core.client.platform.validation.AMFValidationReport;
import amf.core.client.platform.validation.AMFValidationResult;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.ArrayList;
import java.util.List;

public class AmfTool {
    public static void main(String[] args) throws Exception {
        int code = 0;
        try {
            switch (args.length > 0 ? args[0] : "") {
                case "parse" -> parse(args[1], Path.of(args[2]));
                case "dialect" -> dialect(args[1], args[2]);
                case "declarations" -> declarations(args[1]);
                default -> throw new IllegalArgumentException("usage: AmfTool parse URL OUT | dialect DIALECT INSTANCE | declarations URL");
            }
        } catch (Throwable t) {
            Throwable c = t.getCause() != null ? t.getCause() : t;
            System.out.println("{\"error\":" + str(String.valueOf(c)) + "}");
            code = 2;
        }
        System.out.flush();
        System.exit(code); // AMF leaves non-daemon threads behind
    }

    static void parse(String url, Path out) throws Exception {
        AMFConfiguration api = APIConfiguration.API();
        AMFParseResult parsed = api.baseUnitClient().parse(url).get();
        String spec = parsed.sourceSpec() != null ? parsed.sourceSpec().id() : "";
        AMFConfiguration conf = api;
        try {
            conf = APIConfiguration.fromSpec(parsed.sourceSpec());
        } catch (RuntimeException e) {
            // keep the generic configuration
        }
        conf = conf.withRenderOptions(new RenderOptions().withSourceMaps());
        List<AMFValidationResult> results = new ArrayList<>(parsed.results());
        boolean conforms = parsed.conforms();
        AMFValidationReport report = conf.baseUnitClient().validate(parsed.baseUnit()).get();
        results.addAll(report.results());
        conforms = conforms && report.conforms();
        boolean rendered = false;
        if (conforms) {
            BaseUnit transformed = conf.baseUnitClient().transform(parsed.baseUnit(), PipelineId.Editing()).baseUnit();
            Files.writeString(out, conf.baseUnitClient().render(transformed, "application/ld+json"), StandardCharsets.UTF_8);
            rendered = true;
        }
        System.out.println("{\"spec\":" + str(spec) + ",\"conforms\":" + conforms + ",\"rendered\":" + rendered + ",\"results\":" + results(results) + "}");
    }

    static void dialect(String dialectUrl, String instanceUrl) throws Exception {
        AMLConfiguration conf = AMLConfiguration.predefined().withDialect(dialectUrl).get();
        AMFParseResult parsed = conf.baseUnitClient().parse(instanceUrl).get();
        List<AMFValidationResult> results = new ArrayList<>(parsed.results());
        AMFValidationReport report = conf.baseUnitClient().validate(parsed.baseUnit()).get();
        results.addAll(report.results());
        boolean conforms = parsed.conforms() && report.conforms();
        System.out.println("{\"conforms\":" + conforms + ",\"parseErrors\":" + parsed.results().size() + ",\"results\":" + results(results) + "}");
    }

    static void declarations(String url) throws Exception {
        AMFParseResult parsed = APIConfiguration.API().baseUnitClient().parse(url).get();
        BaseUnit unit = APIConfiguration.fromSpec(parsed.sourceSpec()).baseUnitClient().transform(parsed.baseUnit(), PipelineId.Editing()).baseUnit();
        StringBuilder b = new StringBuilder("[");
        if (unit instanceof DeclaresModel declares) {
            for (DomainElement e : declares.declares()) {
                if (b.length() > 1) b.append(',');
                String name = e instanceof NamedDomainElement n && n.name() != null ? n.name().option().orElse("") : "";
                b.append("{\"id\":").append(str(e.id())).append(",\"name\":").append(str(name));
                if (e instanceof NodeShape shape) {
                    b.append(",\"nodeShape\":true,\"properties\":[");
                    boolean first = true;
                    for (PropertyShape p : shape.properties()) {
                        if (!first) b.append(',');
                        first = false;
                        Object min = p.minCount().option().orElse(0);
                        b.append("{\"name\":").append(str(p.name().option().orElse(""))).append(",\"minCount\":").append(min).append('}');
                    }
                    b.append(']');
                }
                b.append('}');
            }
        }
        System.out.println("{\"conforms\":" + parsed.conforms() + ",\"results\":" + results(parsed.results()) + ",\"declarations\":" + b.append(']') + "}");
    }

    static String results(List<AMFValidationResult> rs) {
        StringBuilder b = new StringBuilder("[");
        for (AMFValidationResult r : rs) {
            if (b.length() > 1) b.append(',');
            String range = "";
            if (r.position() != null) {
                range = r.position().toString();
            }
            b.append("{\"validationId\":").append(str(r.validationId()))
             .append(",\"severityLevel\":").append(str(r.severityLevel()))
             .append(",\"message\":").append(str(r.message()))
             .append(",\"targetNode\":").append(str(r.targetNode()))
             .append(",\"targetProperty\":").append(str(r.targetProperty()))
             .append(",\"position\":").append(str(range))
             .append(",\"location\":").append(str(r.location().orElse("")))
             .append('}');
        }
        return b.append(']').toString();
    }

    static String str(String s) {
        if (s == null) return "\"\"";
        StringBuilder b = new StringBuilder("\"");
        for (char c : s.toCharArray()) {
            switch (c) {
                case '"' -> b.append("\\\"");
                case '\\' -> b.append("\\\\");
                case '\n' -> b.append("\\n");
                case '\r' -> b.append("\\r");
                case '\t' -> b.append("\\t");
                default -> {
                    if (c < 0x20) b.append(String.format("\\u%04x", (int) c));
                    else b.append(c);
                }
            }
        }
        return b.append('"').toString();
    }
}
